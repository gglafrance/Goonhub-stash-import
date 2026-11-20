// Command goonhub-stash-import copies a Stash library's metadata into GoonHub.
// Stash is only ever read (GraphQL queries only).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"goonhub-stash-importer/internal/config"
	"goonhub-stash-importer/internal/goonhub"
	"goonhub-stash-importer/internal/httpx"
	"goonhub-stash-importer/internal/importer"
	"goonhub-stash-importer/internal/stash"
	"goonhub-stash-importer/internal/state"
)

const usage = `Usage: goonhub-stash-import [command] [flags]

Commands:
  import   import Stash into GoonHub (default)
  check    read-only preflight: connectivity, storage paths, path-mapping coverage
  verify   compare every selected Stash scene with what GoonHub holds
  process  submit post_import.process phases (thumbnails, sprites...) for the
           imported scenes of the state file (all, or --ids Stash IDs)
  status   print the local import state

Flags:
`

func main() {
	os.Exit(run())
}

func run() int {
	cmd := "import"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage); fs.PrintDefaults() }
	cfgPath := fs.String("config", "config.yaml", "configuration file")
	dryRun := fs.Bool("dry-run", false, "simulate: read everything, write nothing")
	limit := fs.Int("limit", -1, "max number of scenes (overrides scenes.limit)")
	ids := fs.String("ids", "", "comma-separated Stash scene IDs (overrides scenes.ids)")
	onExisting := fs.String("on-existing", "", "skip | merge (overrides on_existing)")
	concurrency := fs.Int("concurrency", 0, "number of workers (overrides concurrency)")
	phases := fs.String("phases", "", "comma-separated phases to run: tags,studios,performers,scenes,markers,interactions")
	logLevel := fs.String("log-level", "", "debug | info | warn | error")
	statePath := fs.String("state", "", "state file (overrides state_file)")
	reportPath := fs.String("report", "", "report file (overrides report_file)")
	apiMode := fs.String("api-mode", "", "auto | v1 | v2 (overrides goonhub.api_mode)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	_ = godotenv.Load() // optional .env for ${VAR} expansion
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if *dryRun {
		cfg.DryRun = true
	}
	if *limit >= 0 {
		cfg.Scenes.Limit = *limit
	}
	if *ids != "" {
		cfg.Scenes.IDs = splitList(*ids)
	}
	if *onExisting != "" {
		cfg.OnExisting = *onExisting
	}
	if *concurrency > 0 {
		cfg.Concurrency = *concurrency
	}
	if *logLevel != "" {
		cfg.Log.Level = *logLevel
	}
	if *statePath != "" {
		cfg.StateFile = *statePath
	}
	if *reportPath != "" {
		cfg.ReportFile = *reportPath
	}
	if *apiMode != "" {
		cfg.GoonHub.APIMode = *apiMode
	}
	if *phases != "" {
		p := map[string]bool{}
		for _, x := range splitList(*phases) {
			p[x] = true
		}
		cfg.Phases = config.PhasesConfig{Tags: p["tags"], Studios: p["studios"], Performers: p["performers"],
			Scenes: p["scenes"], Markers: p["markers"], Interactions: p["interactions"]}
	}
	if cmd == "verify" {
		cfg.ReportFile = strings.TrimSuffix(cfg.ReportFile, ".json") + ".verify.json"
	}
	if cmd == "verify" || cmd == "check" {
		cfg.DryRun = true // never write anything
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLevel(cfg.Log.Level)}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Warn("interrupt received: finishing in-flight requests and saving state (Ctrl-C again to force)")
		cancel()
		<-sigs
		os.Exit(130)
	}()

	st, err := state.Open(cfg.StateFile, cfg.GoonHub.URL, cfg.DryRun)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if cmd == "status" {
		printStatus(st)
		return 0
	}

	retry := httpx.Policy{MaxAttempts: cfg.Retry.MaxAttempts, InitialBackoff: cfg.Retry.InitialBackoff, MaxBackoff: cfg.Retry.MaxBackoff}
	sc := stash.New(cfg.Stash.URL, cfg.Stash.APIKey, cfg.Stash.Timeout, retry)
	gh := goonhub.New(cfg.GoonHub.URL, cfg.GoonHub.Username, cfg.GoonHub.Password, cfg.GoonHub.Timeout, retry)
	rep := importer.NewReport(cfg.DryRun)
	imp := importer.New(cfg, sc, gh, st, rep, log)
	if err := imp.Connect(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	switch cmd {
	case "check":
		problems, err := imp.Check(ctx, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if problems > 0 {
			fmt.Printf("\n%d problem(s) found.\n", problems)
			return 1
		}
		fmt.Println("\nAll checks passed.")
		return 0

	case "verify":
		vs, err := imp.Verify(ctx)
		if vs != nil {
			fmt.Printf("\n=== Verify ===\n  checked %d, ok %d, mismatch %d, not imported %d, missing in GoonHub %d, skipped %d\n",
				vs.Checked.Load(), vs.OK.Load(), vs.Mismatch.Load(), vs.NotImported.Load(), vs.Missing.Load(), vs.Skipped.Load())
		}
		rep.PrintSummary(os.Stdout)
		if werr := rep.WriteFile(cfg.ReportFile); werr != nil {
			log.Error("failed to write report", "err", werr)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if vs.Mismatch.Load()+vs.NotImported.Load()+vs.Missing.Load() > 0 {
			return 1
		}
		return 0

	case "process":
		var ghIDs []uint
		want := map[string]bool{}
		for _, id := range cfg.Scenes.IDs {
			want[id] = true
		}
		st.Each(state.KindScenes, func(id string, e state.Entry) {
			if e.GH != 0 && e.Status != state.StatusFailed && (len(want) == 0 || want[id]) {
				ghIDs = append(ghIDs, e.GH)
			}
		})
		log.Info("submitting processing", "scenes", len(ghIDs), "phases", cfg.PostImport.Process.Phases, "mode", cfg.PostImport.Process.Mode)
		imp.SubmitProcessing(ctx, ghIDs)
		if errs, _ := rep.Counts(); errs > 0 {
			rep.PrintSummary(os.Stdout)
			return 1
		}
		return 0

	case "import":
		if cfg.DryRun {
			log.Info("DRY RUN: nothing will be written to GoonHub nor to the state file")
		}
		saveCtx, stopSave := context.WithCancel(context.Background())
		saved := make(chan struct{})
		go func() {
			st.AutoSave(saveCtx, 5*time.Second, func(err error) { log.Error("failed to save state", "err", err) })
			close(saved)
		}()
		runErr := imp.Run(ctx)
		if runErr == nil {
			imp.PostImport(ctx)
		}
		stopSave()
		<-saved
		rep.PrintSummary(os.Stdout)
		if err := rep.WriteFile(cfg.ReportFile); err != nil {
			log.Error("failed to write report", "err", err)
		} else if cfg.ReportFile != "" {
			fmt.Printf("\nReport: %s   State: %s\n", cfg.ReportFile, cfg.StateFile)
		}
		if runErr != nil {
			fmt.Fprintln(os.Stderr, "import stopped:", runErr)
			return 2
		}
		if errs, _ := rep.Counts(); errs > 0 {
			fmt.Println("Re-run the same command to retry failed/partial entities.")
			return 1
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
	fs.Usage()
	return 2
}

func printStatus(st *state.Store) {
	fmt.Println("Import state:")
	for _, k := range state.Kinds {
		c := st.Counts(k)
		fmt.Printf("  %-11s ok %-7d partial %-7d failed %d\n", k, c[state.StatusOK], c[state.StatusPartial], c[state.StatusFailed])
	}
	shown := 0
	for _, k := range state.Kinds {
		st.Each(k, func(id string, e state.Entry) {
			if e.Status == state.StatusOK || shown >= 20 {
				return
			}
			shown++
			fmt.Printf("    %s stash:%s gh:%d %s: %s\n", k, id, e.GH, e.Status, e.Err)
		})
	}
}

func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

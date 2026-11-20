package importer

import (
	"context"
	"fmt"
	"io"
	"sort"
)

// Check is a read-only preflight: it walks every selected Stash scene and
// reports path-mapping coverage and data problems, without touching GoonHub.
func (imp *Importer) Check(ctx context.Context, w io.Writer) (problems int, err error) {
	sq := imp.sceneQuery()
	sq.Fields = `id date files { path } scene_markers { id }`
	if sq.PerPage < 1000 {
		sq.PerPage = 1000
	}
	perMapping := map[string]int{}
	var unmapped []string
	unmappedCount, badDates, multi, markers, scenes, noFile := 0, 0, 0, 0, 0, 0
	for page := 1; ; page++ {
		list, total, err := imp.stash.ScenesPage(ctx, sq, page)
		if err != nil {
			return 0, err
		}
		for _, sc := range list {
			if imp.cfg.Scenes.Limit > 0 && scenes >= imp.cfg.Scenes.Limit {
				break
			}
			scenes++
			markers += len(sc.SceneMarkers)
			if len(sc.Files) == 0 {
				noFile++
				continue
			}
			if len(sc.Files) > 1 {
				multi++
			}
			if _, ok := normalizeDate(sc.Date); !ok {
				badDates++
			}
			m, err := imp.pm.Map(sc.Files[0].Path)
			if err != nil {
				unmappedCount++
				if len(unmapped) < 10 {
					unmapped = append(unmapped, sc.Files[0].Path)
				}
				continue
			}
			perMapping[m.Mapping.StashPrefix+" -> "+m.Mapping.GoonHubPrefix]++
		}
		if len(list) == 0 || page*sq.PerPage >= total || (imp.cfg.Scenes.Limit > 0 && scenes >= imp.cfg.Scenes.Limit) {
			break
		}
	}
	fmt.Fprintf(w, "\nScenes selected: %d (markers: %d, multi-file: %d, without file: %d, unparsable dates: %d)\n", scenes, markers, multi, noFile, badDates)
	keys := make([]string, 0, len(perMapping))
	for k := range perMapping {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "  %-60s %d\n", k, perMapping[k])
	}
	if unmappedCount > 0 {
		fmt.Fprintf(w, "  UNMAPPED: %d scenes (policy: %s), e.g.:\n", unmappedCount, imp.cfg.Scenes.UnmappedPath)
		for _, p := range unmapped {
			fmt.Fprintf(w, "    %s\n", p)
		}
		if imp.cfg.Scenes.UnmappedPath == "error" {
			problems += unmappedCount
		}
	}
	problems += noFile
	return problems, nil
}

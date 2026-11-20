package pathmap

import (
	"testing"

	"goonhub-stash-importer/internal/config"
)

func TestMap(t *testing.T) {
	m := New([]config.PathMapping{
		{StashPrefix: "/data/", GoonHubPrefix: "/media/stash/data/", StoragePathID: 2},
		{StashPrefix: "/data2", GoonHubPrefix: "/media/stash/data2", StoragePathID: 3},
		{StashPrefix: "/data2/special", GoonHubPrefix: "/media/special", StoragePathID: 5},
		{StashPrefix: "/coomer-dl", GoonHubPrefix: "/media/stash/coomer-dl", StoragePathID: 4},
	})
	tests := []struct {
		in   string
		want string
		sp   uint
		err  bool
	}{
		{"/data/a/b.mp4", "/media/stash/data/a/b.mp4", 2, false},
		{"/data2/x.mp4", "/media/stash/data2/x.mp4", 3, false}, // must not match "/data/"
		{"/data2/special/y.mp4", "/media/special/y.mp4", 5, false},
		{"/coomer-dl/u/z.mp4", "/media/stash/coomer-dl/u/z.mp4", 4, false},
		{"/data3/z.mp4", "", 0, true},
		{"/datax.mp4", "", 0, true},
	}
	for _, tt := range tests {
		got, err := m.Map(tt.in)
		if tt.err {
			if err == nil {
				t.Fatalf("Map(%q): expected error, got %+v", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Map(%q): unexpected error %v", tt.in, err)
		}
		if got.Path != tt.want || got.StoragePathID != tt.sp {
			t.Fatalf("Map(%q) = %q/%d, want %q/%d", tt.in, got.Path, got.StoragePathID, tt.want, tt.sp)
		}
	}
}

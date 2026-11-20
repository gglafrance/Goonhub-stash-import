package goonhub

type Tag struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type NamedItem struct {
	ID      uint     `json:"id"`
	UUID    string   `json:"uuid"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
}

type StoragePath struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDefault bool   `json:"is_default"`
}

type StudioRequest struct {
	Name        string   `json:"name"`
	URL         string   `json:"url,omitempty"`
	Description string   `json:"description,omitempty"`
	Rating      *float64 `json:"rating,omitempty"`
	ParentID    *uint    `json:"parent_id,omitempty"`
}

type ActorRequest struct {
	Name            string   `json:"name"`
	Aliases         []string `json:"aliases,omitempty"`
	Gender          string   `json:"gender,omitempty"`
	Birthday        *string  `json:"birthday,omitempty"`
	DateOfDeath     *string  `json:"date_of_death,omitempty"`
	Ethnicity       string   `json:"ethnicity,omitempty"`
	Nationality     string   `json:"nationality,omitempty"`
	CareerStartYear *int     `json:"career_start_year,omitempty"`
	CareerEndYear   *int     `json:"career_end_year,omitempty"`
	HeightCm        *int     `json:"height_cm,omitempty"`
	WeightKg        *int     `json:"weight_kg,omitempty"`
	Measurements    string   `json:"measurements,omitempty"`
	HairColor       string   `json:"hair_color,omitempty"`
	EyeColor        string   `json:"eye_color,omitempty"`
	Tattoos         string   `json:"tattoos,omitempty"`
	Piercings       string   `json:"piercings,omitempty"`
	FakeBoobs       bool     `json:"fake_boobs,omitempty"`
}

type Watch struct {
	WatchedAt     string `json:"watched_at"`
	WatchDuration int    `json:"watch_duration"`
	LastPosition  int    `json:"last_position"`
	Completed     bool   `json:"completed"`
}

type Interactions struct {
	Rating      *float64 `json:"rating,omitempty"`
	Liked       *bool    `json:"liked,omitempty"`
	JizzedCount *int     `json:"jizzed_count,omitempty"`
	JizzHistory []string `json:"jizz_history,omitempty"`
	Watches     []Watch  `json:"watches,omitempty"`
}

// ImportSceneRequest is the v2 payload; v1 servers ignore the v2-only fields.
type ImportSceneRequest struct {
	Title            string        `json:"title"`
	StoredPath       string        `json:"stored_path"`
	OriginalFilename string        `json:"original_filename,omitempty"`
	Size             int64         `json:"size,omitempty"`
	Duration         int           `json:"duration,omitempty"`
	Width            int           `json:"width,omitempty"`
	Height           int           `json:"height,omitempty"`
	FrameRate        float64       `json:"frame_rate,omitempty"`
	BitRate          int64         `json:"bit_rate,omitempty"`
	VideoCodec       string        `json:"video_codec,omitempty"`
	AudioCodec       string        `json:"audio_codec,omitempty"`
	Description      string        `json:"description,omitempty"`
	ReleaseDate      *string       `json:"release_date,omitempty"`
	StudioID         *uint         `json:"studio_id,omitempty"`
	StoragePathID    *uint         `json:"storage_path_id,omitempty"`
	Origin           string        `json:"origin,omitempty"`
	Type             string        `json:"type,omitempty"`
	SkipFileCheck    bool          `json:"skip_file_check,omitempty"`
	TagIDs           []uint        `json:"tag_ids"`
	ActorIDs         []uint        `json:"actor_ids"`
	ViewCount        *int64        `json:"view_count,omitempty"`
	CreatedAt        *string       `json:"created_at,omitempty"`
	FileCreatedAt    *string       `json:"file_created_at,omitempty"`
	UpdateExisting   bool          `json:"update_existing,omitempty"`
	SkipIndex        bool          `json:"skip_index,omitempty"`
	Interactions     *Interactions `json:"interactions,omitempty"`
}

type ImportMarkerRequest struct {
	SceneID        uint   `json:"scene_id"`
	UserID         uint   `json:"user_id,omitempty"`
	Timestamp      int    `json:"timestamp"`
	EndTimestamp   *int   `json:"end_timestamp,omitempty"`
	Label          string `json:"label,omitempty"`
	Color          string `json:"color,omitempty"`
	TagIDs         []uint `json:"tag_ids,omitempty"`
	UpdateExisting bool   `json:"update_existing,omitempty"`
}

type Marker struct {
	ID        uint   `json:"id"`
	Timestamp int    `json:"timestamp"`
	Label     string `json:"label"`
	Color     string `json:"color"`
}

// SceneSummary is returned by the v2 summary endpoint (and rebuilt from several
// calls in v1 mode).
type SceneSummary struct {
	ID          uint    `json:"id"`
	Title       string  `json:"title"`
	StoredPath  string  `json:"stored_path"`
	StudioID    *uint   `json:"studio_id"`
	TagIDs      []uint  `json:"tag_ids"`
	ActorIDs    []uint  `json:"actor_ids"`
	ViewCount   int64   `json:"view_count"`
	MarkerCount int     `json:"marker_count"`
	Rating      float64 `json:"rating"`
	Liked       bool    `json:"liked"`
	JizzedCount int     `json:"jizzed_count"`
	WatchCount  int     `json:"watch_count"`
	Trashed     bool    `json:"trashed"`
}

type Capabilities struct {
	Version  int      `json:"version"`
	Features []string `json:"features"`
}

func (c Capabilities) Has(f string) bool {
	for _, x := range c.Features {
		if x == f {
			return true
		}
	}
	return false
}

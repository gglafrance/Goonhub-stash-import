package main

// GoonHub API request/response types

// --- Auth ---

type GHLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type GHLoginResponse struct {
	Token string `json:"token"`
	User  struct {
		ID       uint   `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	} `json:"user"`
}

// --- Tags ---

type GHTag struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type GHTagWithCount struct {
	GHTag
	SceneCount int64 `json:"scene_count"`
}

type GHCreateTagRequest struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// --- Studios ---

type GHStudio struct {
	ID          uint     `json:"id"`
	Name        string   `json:"name"`
	ShortName   string   `json:"short_name"`
	URL         string   `json:"url"`
	Description string   `json:"description"`
	Rating      *float64 `json:"rating"`
	ParentID    *uint    `json:"parent_id"`
	NetworkID   *uint    `json:"network_id"`
}

type GHStudioListItem struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	ShortName  string `json:"short_name"`
	SceneCount int64  `json:"scene_count"`
}

type GHCreateStudioRequest struct {
	Name        string   `json:"name"`
	URL         string   `json:"url,omitempty"`
	Description string   `json:"description,omitempty"`
	Rating      *float64 `json:"rating,omitempty"`
}

type GHUpdateStudioRequest struct {
	ParentID *uint `json:"parent_id"`
}

// --- Actors ---

type GHActor struct {
	ID           uint    `json:"id"`
	Name         string  `json:"name"`
	Gender       string  `json:"gender"`
	Birthday     *string `json:"birthday"`
	DateOfDeath  *string `json:"date_of_death"`
	Ethnicity    string  `json:"ethnicity"`
	Nationality  string  `json:"nationality"`
	HeightCm     *int    `json:"height_cm"`
	WeightKg     *int    `json:"weight_kg"`
	Measurements string  `json:"measurements"`
	HairColor    string  `json:"hair_color"`
	EyeColor     string  `json:"eye_color"`
	Tattoos      string  `json:"tattoos"`
	Piercings    string  `json:"piercings"`
	FakeBoobs    bool    `json:"fake_boobs"`
}

type GHActorListItem struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	Gender     string `json:"gender"`
	SceneCount int64  `json:"scene_count"`
}

type GHCreateActorRequest struct {
	Name         string  `json:"name"`
	Gender       string  `json:"gender,omitempty"`
	Birthday     *string `json:"birthday,omitempty"`
	DateOfDeath  *string `json:"date_of_death,omitempty"`
	Ethnicity    string  `json:"ethnicity,omitempty"`
	Nationality  string  `json:"nationality,omitempty"`
	HeightCm     *int    `json:"height_cm,omitempty"`
	WeightKg     *int    `json:"weight_kg,omitempty"`
	Measurements string  `json:"measurements,omitempty"`
	HairColor    string  `json:"hair_color,omitempty"`
	EyeColor     string  `json:"eye_color,omitempty"`
	Tattoos      string  `json:"tattoos,omitempty"`
	Piercings    string  `json:"piercings,omitempty"`
	FakeBoobs    bool    `json:"fake_boobs,omitempty"`
}

// --- Scenes (Import) ---

type GHImportSceneRequest struct {
	Title            string  `json:"title"`
	StoredPath       string  `json:"stored_path"`
	OriginalFilename string  `json:"original_filename,omitempty"`
	Size             int64   `json:"size,omitempty"`
	Duration         int     `json:"duration,omitempty"`
	Width            int     `json:"width,omitempty"`
	Height           int     `json:"height,omitempty"`
	FrameRate        float64 `json:"frame_rate,omitempty"`
	BitRate          int64   `json:"bit_rate,omitempty"`
	VideoCodec       string  `json:"video_codec,omitempty"`
	AudioCodec       string  `json:"audio_codec,omitempty"`
	Description      string  `json:"description,omitempty"`
	ReleaseDate      *string `json:"release_date,omitempty"`
	StudioID         *uint   `json:"studio_id,omitempty"`
	StoragePathID    *uint   `json:"storage_path_id,omitempty"`
	Origin           string  `json:"origin,omitempty"`
	Type             string  `json:"type,omitempty"`
	SkipFileCheck    bool    `json:"skip_file_check,omitempty"`
}

type GHImportSceneResponse struct {
	ID    uint   `json:"id"`
	Title string `json:"title"`
}

// --- Markers (Import) ---

type GHImportMarkerRequest struct {
	SceneID   uint   `json:"scene_id"`
	UserID    uint   `json:"user_id"`
	Timestamp int    `json:"timestamp"`
	Label     string `json:"label,omitempty"`
	Color     string `json:"color,omitempty"`
}

type GHImportMarkerResponse struct {
	ID      uint `json:"id"`
	SceneID uint `json:"scene_id"`
}

// --- Associations ---

type GHSetTagsRequest struct {
	TagIDs []uint `json:"tag_ids"`
}

type GHSetActorsRequest struct {
	ActorIDs []uint `json:"actor_ids"`
}

type GHSetStudioRequest struct {
	StudioID uint `json:"studio_id"`
}

type GHSetMarkerTagsRequest struct {
	TagIDs []uint `json:"tag_ids"`
}

// --- Generic paginated response ---

type GHPaginatedResponse[T any] struct {
	Data       []T `json:"data"`
	Pagination struct {
		Page       int   `json:"page"`
		Limit      int   `json:"limit"`
		TotalItems int64 `json:"total_items"`
		TotalPages int   `json:"total_pages"`
	} `json:"pagination"`
}

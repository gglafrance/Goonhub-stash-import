package main

// Stash GraphQL response types

type StashTag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type StashStudio struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	URLs         []string    `json:"urls"`
	Details      string      `json:"details"`
	Rating100    *int        `json:"rating100"`
	ParentStudio *StashIDRef `json:"parent_studio"`
}

type StashIDRef struct {
	ID string `json:"id"`
}

type StashPerformer struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Gender       *string `json:"gender"`
	Birthdate    *string `json:"birthdate"`
	DeathDate    *string `json:"death_date"`
	Ethnicity    *string `json:"ethnicity"`
	Country      *string `json:"country"`
	EyeColor     *string `json:"eye_color"`
	HeightCm     *int    `json:"height_cm"`
	Measurements *string `json:"measurements"`
	FakeTits     *string `json:"fake_tits"`
	Tattoos      *string `json:"tattoos"`
	Piercings    *string `json:"piercings"`
	HairColor    *string `json:"hair_color"`
	Weight       *int    `json:"weight"`
	ImagePath    *string `json:"image_path"`
}

type StashScene struct {
	ID           string          `json:"id"`
	Title        *string         `json:"title"`
	Details      *string         `json:"details"`
	Date         *string         `json:"date"`
	Rating100    *int            `json:"rating100"`
	OCounter     *int            `json:"o_counter"`
	Files        []StashFile     `json:"files"`
	Studio       *StashIDRef     `json:"studio"`
	Performers   []StashIDRef    `json:"performers"`
	Tags         []StashIDRef    `json:"tags"`
	SceneMarkers []StashMarker   `json:"scene_markers"`
}

type StashFile struct {
	Path       string  `json:"path"`
	Size       int64   `json:"size"`
	Duration   float64 `json:"duration"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	VideoCodec string  `json:"video_codec"`
	AudioCodec string  `json:"audio_codec"`
	FrameRate  float64 `json:"frame_rate"`
	BitRate    int64   `json:"bit_rate"`
	Basename   string  `json:"basename"`
}

type StashMarker struct {
	ID         string       `json:"id"`
	Title      string       `json:"title"`
	Seconds    float64      `json:"seconds"`
	PrimaryTag *StashIDRef  `json:"primary_tag"`
	Tags       []StashIDRef `json:"tags"`
}

// GraphQL response wrappers

type graphqlResponse[T any] struct {
	Data   T              `json:"data"`
	Errors []graphqlError `json:"errors,omitempty"`
}

type graphqlError struct {
	Message string `json:"message"`
}

type findTagsData struct {
	FindTags struct {
		Tags []StashTag `json:"tags"`
	} `json:"findTags"`
}

type findStudiosData struct {
	FindStudios struct {
		Studios []StashStudio `json:"studios"`
	} `json:"findStudios"`
}

type findPerformersData struct {
	FindPerformers struct {
		Performers []StashPerformer `json:"performers"`
	} `json:"findPerformers"`
}

type findScenesData struct {
	FindScenes struct {
		Scenes []StashScene `json:"scenes"`
	} `json:"findScenes"`
}

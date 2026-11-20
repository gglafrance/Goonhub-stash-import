package stash

type IDRef struct {
	ID string `json:"id"`
}

type NamedRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Tag struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description *string    `json:"description"`
	Aliases     []string   `json:"aliases"`
	Parents     []NamedRef `json:"parents"`
}

type Studio struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	URLs         []string `json:"urls"`
	Details      *string  `json:"details"`
	Rating100    *int     `json:"rating100"`
	Favorite     bool     `json:"favorite"`
	Aliases      []string `json:"aliases"`
	ImagePath    *string  `json:"image_path"`
	ParentStudio *IDRef   `json:"parent_studio"`
}

type Performer struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Disambiguation *string  `json:"disambiguation"`
	AliasList      []string `json:"alias_list"`
	Gender         *string  `json:"gender"`
	Birthdate      *string  `json:"birthdate"`
	DeathDate      *string  `json:"death_date"`
	Ethnicity      *string  `json:"ethnicity"`
	Country        *string  `json:"country"`
	EyeColor       *string  `json:"eye_color"`
	HairColor      *string  `json:"hair_color"`
	HeightCm       *int     `json:"height_cm"`
	Weight         *int     `json:"weight"`
	Measurements   *string  `json:"measurements"`
	FakeTits       *string  `json:"fake_tits"`
	Tattoos        *string  `json:"tattoos"`
	Piercings      *string  `json:"piercings"`
	CareerStart    *string  `json:"career_start"`
	CareerEnd      *string  `json:"career_end"`
	Favorite       bool     `json:"favorite"`
	Rating100      *int     `json:"rating100"`
	ImagePath      *string  `json:"image_path"`
}

type VideoFile struct {
	Path       string  `json:"path"`
	Basename   string  `json:"basename"`
	Size       int64   `json:"size"`
	Duration   float64 `json:"duration"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	VideoCodec string  `json:"video_codec"`
	AudioCodec string  `json:"audio_codec"`
	FrameRate  float64 `json:"frame_rate"`
	BitRate    int64   `json:"bit_rate"`
	ModTime    *string `json:"mod_time"`
}

type Marker struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Seconds    float64   `json:"seconds"`
	EndSeconds *float64  `json:"end_seconds"`
	PrimaryTag *NamedRef `json:"primary_tag"`
	Tags       []IDRef   `json:"tags"`
}

type ScenePaths struct {
	Screenshot *string `json:"screenshot"`
}

type Scene struct {
	ID           string      `json:"id"`
	Title        *string     `json:"title"`
	Code         *string     `json:"code"`
	Details      *string     `json:"details"`
	Director     *string     `json:"director"`
	URLs         []string    `json:"urls"`
	Date         *string     `json:"date"`
	Rating100    *int        `json:"rating100"`
	Organized    bool        `json:"organized"`
	OCounter     *int        `json:"o_counter"`
	PlayCount    *int        `json:"play_count"`
	PlayHistory  []string    `json:"play_history"`
	OHistory     []string    `json:"o_history"`
	ResumeTime   *float64    `json:"resume_time"`
	PlayDuration *float64    `json:"play_duration"`
	CreatedAt    string      `json:"created_at"`
	Files        []VideoFile `json:"files"`
	Paths        ScenePaths  `json:"paths"`
	Studio       *IDRef      `json:"studio"`
	Performers   []IDRef     `json:"performers"`
	Tags         []IDRef     `json:"tags"`
	SceneMarkers []Marker    `json:"scene_markers"`
}

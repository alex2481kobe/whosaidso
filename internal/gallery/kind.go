package gallery

// What a gallery file previews as, decided by its extension alone and in
// this one table: the viewer serves only these kinds, with these types, and
// draws each kind its own way. Anything else is "no preview" and is never
// served, so a file that a browser would run (HTML, SVG) is never handed to
// one.

import (
	"os"
	"path/filepath"
	"strings"
)

// Shown is an entry as it stands now: its preview kind and whether its file
// is still there, looked at when asked, never remembered.
type Shown struct {
	Entry
	Kind   string `json:"kind"`
	Status string `json:"status"` // "present" or "missing"
}

// List is every entry with its kind and its status at this moment. A file
// moved to the Trash, renamed, or replaced by a directory is missing.
func List(file string) ([]Shown, error) {
	entries, err := Load(file)
	if err != nil {
		return nil, err
	}
	shown := make([]Shown, 0, len(entries))
	for _, e := range entries {
		kind, _ := Kind(e.Path)
		status := "missing"
		if info, err := os.Stat(e.Path); err == nil && info.Mode().IsRegular() {
			status = "present"
		}
		shown = append(shown, Shown{Entry: e, Kind: kind, Status: status})
	}
	return shown, nil
}

// Preview kinds.
const (
	Image = "image"
	Video = "video"
	Text  = "text"
	None  = ""
)

var kinds = map[string][2]string{
	".png":  {Image, "image/png"},
	".jpg":  {Image, "image/jpeg"},
	".jpeg": {Image, "image/jpeg"},
	".gif":  {Image, "image/gif"},
	".webp": {Image, "image/webp"},
	".mp4":  {Video, "video/mp4"},
	".mov":  {Video, "video/quicktime"},
	".txt":  {Text, "text/plain; charset=utf-8"},
	".log":  {Text, "text/plain; charset=utf-8"},
	".md":   {Text, "text/markdown; charset=utf-8"},
	".json": {Text, "application/json; charset=utf-8"},
	".csv":  {Text, "text/csv; charset=utf-8"},
}

// Kind is the preview kind of path and the media type it is served with;
// None and "" for a file the viewer does not preview.
func Kind(path string) (kind, mediaType string) {
	k := kinds[strings.ToLower(filepath.Ext(path))]
	return k[0], k[1]
}

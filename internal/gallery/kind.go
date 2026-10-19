package gallery

// What a gallery file previews as, decided by its extension alone and in
// this one table: the viewer serves only these kinds, with these types, and
// draws each kind its own way. Anything else is "no preview" and is never
// served, so a file that a browser would run (HTML, SVG) is never handed to
// one.

import (
	"path/filepath"
	"strings"
)

// Shown combines the admitted entry with a local availability observation.
type Shown struct {
	Entry
	Kind   string `json:"kind"`
	Status string `json:"status"`
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

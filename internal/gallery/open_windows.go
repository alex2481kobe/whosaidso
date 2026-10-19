// Windows media opening follows the regular-file precheck in gallery.go.
// File identity and hashing remain platform independent.
package gallery

import "os"

func openMedia(path string) (*os.File, error) { return os.Open(path) }

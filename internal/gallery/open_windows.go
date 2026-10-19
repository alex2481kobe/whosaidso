// Windows media opens use the same root containment as other platforms.
// File identity and hashing remain platform independent.
package gallery

import "os"

func openMedia(root *os.Root, path string) (*os.File, error) {
	if root != nil {
		return root.Open(path)
	}
	return os.Open(path)
}

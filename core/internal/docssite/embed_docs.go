//go:build embeddata

package docssite

import (
	"embed"
	"io/fs"
)

//go:embed all:public
var rawFS embed.FS

func docsFS() (fs.FS, error) {
	return fs.Sub(rawFS, "public")
}

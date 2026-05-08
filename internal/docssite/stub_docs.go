//go:build !embeddata

package docssite

import (
	"fmt"
	"io/fs"
)

func docsFS() (fs.FS, error) {
	return nil, fmt.Errorf(
		"documentation is not embedded in this build\n" +
			"Use a release binary, or run: make build-full",
	)
}

package docssite

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
)

// Serve starts an HTTP server on port and serves the embedded documentation.
// Returns an error immediately if documentation was not embedded at build time.
func Serve(port int, openBrowser bool) error {
	site, err := docsFS()
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("listen :%d: %w", port, err)
	}

	url := fmt.Sprintf("http://localhost:%d", ln.Addr().(*net.TCPAddr).Port)
	fmt.Fprintf(os.Stdout, "DPG documentation: %s\n", url)
	fmt.Fprintf(os.Stdout, "Press Ctrl+C to stop.\n")

	if openBrowser {
		go launchBrowser(url)
	}

	return http.Serve(ln, http.FileServer(http.FS(site)))
}

func launchBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}

package api

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"nyatunnel-server/internal/server/webdist"
)

// webHandler serves the single-page web console.
//
// It prefers the real build in dir (checked per request, so rebuilding the web app
// while the server runs just works) and falls back to the embedded placeholder page.
// Unknown paths without a file extension fall back to index.html for client-side routing.
func webHandler(dir string) http.Handler {
	embedded, err := fs.Sub(webdist.FS, "static")
	if err != nil {
		panic(err) // the embedded tree is fixed at build time
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
			return
		}
		root := fs.FS(embedded)
		if dir != "" {
			if st, err := os.Stat(path.Join(dir, "index.html")); err == nil && !st.IsDir() {
				root = os.DirFS(dir)
			}
		}

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) || !isFile(root, name) {
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		serveFile(w, r, root, name)
	})
}

func isFile(root fs.FS, name string) bool {
	st, err := fs.Stat(root, name)
	return err == nil && !st.IsDir()
}

func serveFile(w http.ResponseWriter, r *http.Request, root fs.FS, name string) {
	f, err := root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, "unsupported file", http.StatusInternalServerError)
		return
	}
	// Vite fingerprints everything under assets/, so it can be cached forever; the shell must revalidate.
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	// ServeContent (unlike ServeFile) does not redirect /index.html to /.
	http.ServeContent(w, r, name, st.ModTime(), rs)
}

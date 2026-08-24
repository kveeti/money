package endpoints

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"money/backend/state"
)

// FrontendHandler serves the SPA on every non-API route. Document navigations
// (the app shell / deep links) pass through the session gate first: no session
// means the user never loads the app, they are bounced straight to login. Assets
// and the HMR websocket are served without a gate.
//
// With DevViteUrl set it reverse-proxies to the Vite dev server (HMR); otherwise
// it serves the embedded build, falling back to index.html for client routes.
func FrontendHandler(st *state.State, dist fs.FS) http.Handler {
	var proxy http.Handler
	if st.Config.DevViteUrl != "" {
		target, err := url.Parse(st.Config.DevViteUrl)
		if err != nil {
			panic(fmt.Errorf("invalid DEV_VITE_URL: %w", err))
		}
		proxy = httputil.NewSingleHostReverseProxy(target)
	}
	files := http.FileServer(http.FS(dist))

	loginURL := "/login"
	if st.Config.DemoMode {
		loginURL += "?demo=1"
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isNavigation(r) {
			_, authErr := Authenticate(st, r)
			if r.URL.Path == "/login" {
				if authErr == nil {
					http.Redirect(w, r, "/transactions", http.StatusFound)
					return
				}
				if r.URL.RequestURI() != loginURL {
					http.Redirect(w, r, loginURL, http.StatusFound)
					return
				}
			} else if authErr != nil {
				http.Redirect(w, r, loginURL, http.StatusFound)
				return
			}
		}

		if proxy != nil {
			proxy.ServeHTTP(w, r)
			return
		}

		if isNavigation(r) {
			serveIndex(w, dist)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			files.ServeHTTP(&immutableAssetWriter{ResponseWriter: w}, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// isNavigation reports whether the request is a top-level document load (a page
// navigation) as opposed to an asset fetch or the HMR websocket.
func isNavigation(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Dest") == "document" {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

type immutableAssetWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *immutableAssetWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if (status >= 200 && status < 300) || status == http.StatusNotModified {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Del("Cache-Control")
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *immutableAssetWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *immutableAssetWriter) ReadFrom(source io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(source)
	}
	return io.Copy(w.ResponseWriter, source)
}

func serveIndex(w http.ResponseWriter, dist fs.FS) {
	f, err := dist.Open("index.html")
	if err != nil {
		http.Error(w, "frontend not built", http.StatusNotFound)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.Copy(w, f)
}

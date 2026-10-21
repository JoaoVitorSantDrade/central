// Package web serve o painel local (spec §8): HTML, CSS e JS embutidos, sem
// nada carregado de fora. O painel lê e escreve só pela API /v1.
package web

import (
	"embed"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
)

//go:embed static
var arquivos embed.FS

// Handler serve o painel em / e /static/ e repassa /v1/ para api. Recusa
// (403) Host que não seja loopback, contra DNS rebinding, e escrita vinda de
// uma página de outra origem, contra CSRF; clientes sem Origin (a CLI) passam.
func Handler(api http.Handler) http.Handler {
	static, err := fs.Sub(arquivos, "static")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", api)
	mux.Handle("GET /static/", http.StripPrefix("/static/", semCache(http.FileServerFS(static))))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, static, "index.html")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostLocal(r.Host) {
			http.Error(w, "host não permitido: o painel só atende em loopback", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !mesmaOrigem(r) {
			http.Error(w, "origem não permitida", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// semCache pede ao navegador que revalide os arquivos (o binário novo troca o painel).
func semCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// hostLocal diz se o Host da requisição é um nome de loopback.
func hostLocal(host string) bool {
	if host == "" {
		return true
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// mesmaOrigem aceita escrita sem Origin (clientes fora do navegador) ou com
// Origin igual ao Host da própria requisição.
func mesmaOrigem(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

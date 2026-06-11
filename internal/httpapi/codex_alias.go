package httpapi

import (
	"net/http"
	"strings"
)

const codexV1AliasPrefix = "/backend-api/codex/v1/"
const codexCanonicalPrefix = "/backend-api/codex/"

func codexV1AliasMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, codexV1AliasPrefix) {
			clone := r.Clone(r.Context())
			clone.URL.Path = codexCanonicalPrefix + r.URL.Path[len(codexV1AliasPrefix):]
			if clone.URL.RawPath != "" && strings.HasPrefix(clone.URL.RawPath, codexV1AliasPrefix) {
				clone.URL.RawPath = codexCanonicalPrefix + clone.URL.RawPath[len(codexV1AliasPrefix):]
			}
			r = clone
		}
		next.ServeHTTP(w, r)
	})
}

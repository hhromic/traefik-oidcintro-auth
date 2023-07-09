// Copyright 2023 Hugo Hromic
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hhromic/traefik-fwdauth/v2/internal/client"
	"github.com/hhromic/traefik-fwdauth/v2/internal/metrics"
	"golang.org/x/exp/slog"
)

// AuthHandler is an http.Handler for authentication requests.
func AuthHandler(isrv *client.IntrospectionService) http.Handler {
	handleErr := func(w http.ResponseWriter, err error, status int) {
		http.Error(w, err.Error(), status)
		slog.Error("auth handler error", "err", err, "status", status)
		metrics.AuthRequestErrors.Add(1)
	}
	handleUnauthorized := func(w http.ResponseWriter, err error) {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		slog.Debug("unauthorized auth request", "err", err)
		metrics.AuthRequestUnauthorized.Add(1)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := time.Now()
		defer func() {
			d := time.Since(s)
			slog.Debug("auth request completed", "duration", d)
			metrics.AuthRequestDuration.Observe(d.Seconds())
		}()

		ahdr := r.Header.Get(HeaderAuthorization)
		if ahdr == "" {
			handleUnauthorized(w,
				fmt.Errorf("%w: %q", ErrMissingRequestHeader, HeaderAuthorization),
			)
			return
		}
		if len(ahdr) <= 7 || strings.ToUpper(ahdr[0:6]) != "BEARER" {
			handleUnauthorized(w, ErrUnsupportedAuthScheme)
			return
		}
		token := ahdr[7:]

		q := r.URL.Query()
		tokenTypeHint := q.Get("token_type_hint")
		clientIDs := make(map[string]struct{})
		for _, cid := range q["client_id"] {
			clientIDs[cid] = struct{}{}
		}

		ires, err := isrv.Introspect(r.Context(), token, tokenTypeHint)
		if err != nil {
			handleErr(w, fmt.Errorf("introspect: %w", err), http.StatusBadGateway)
			return
		}

		if !ires.Active {
			http.Error(w, "token inactive", http.StatusUnauthorized)
			return
		}

		if len(clientIDs) > 0 && ires.ClientID != "" {
			if _, ok := clientIDs[ires.ClientID]; !ok {
				http.Error(w, "invalid client ID", http.StatusUnauthorized)
				return
			}
		}

		if ires.ClientID != "" {
			w.Header().Set(HeaderXForwardedClientID, ires.ClientID)
		}

		if ires.Scope != "" {
			w.Header().Set(HeaderXForwardedScope, ires.Scope)
		}

		if ires.Subject != "" {
			w.Header().Set(HeaderXForwardedSubject, ires.Subject)
		}
	})
}
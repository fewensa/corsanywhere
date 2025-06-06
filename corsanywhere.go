package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

var (
	flags           = flag.NewFlagSet("corsanywhere", flag.ExitOnError)
	fPort           = flags.String("port", "8080", "Local port to listen for this corsanywhere service")
	fRequireOrigin  = flags.Bool("require-origin", true, "Require Origin header on requests")
	fEnableRedirect = flags.Bool("enable-redirect", false, "Auto follow 307/308 redirect")
	fMaxRedirects   = flags.Int("max-redirects", 3, "Maximum number of redirects to follow")
	fTimeout        = flags.Int("timeout", 30, "Timeout (seconds) for HTTP client and transport")
)

func main() {
	flags.Parse(os.Args[1:])

	port := *fPort
	requireOrigin := *fRequireOrigin
	timeout := time.Duration(*fTimeout) * time.Second

	fmt.Printf("CORS Anywhere started at http://localhost:%s\n", port)

	err := http.ListenAndServe(fmt.Sprintf(":%s", port), CORSAnywhereHandler(requireOrigin, timeout))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func CORSAnywhereHandler(requireOrigin bool, timeout time.Duration) http.Handler {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(corsAnywhereUsage))
	})
	r.Handle("/*", corsProxy(requireOrigin, timeout))
	return r
}

func hasScheme(rawurl string) bool {
	return strings.HasPrefix(rawurl, "http://") || strings.HasPrefix(rawurl, "https://")
}

func corsProxy(requireOrigin bool, timeout time.Duration) http.Handler {
	enableRedirect := *fEnableRedirect
	maxRedirects := *fMaxRedirects

	director := func(req *http.Request) {
		corsURL := chi.URLParam(req, "*")
		if !hasScheme(corsURL) {
			corsURL = "http://" + corsURL
		}
		u, err := url.Parse(corsURL)
		if err != nil {
			return
		}

		req.URL.Scheme = u.Scheme
		req.URL.Host = u.Host
		req.URL.Path = u.Path
		req.Host = u.Host

		// NOTE: the req.Query will already be set properly for us

		req.Header.Del("set-cookie")
		req.Header.Del("set-cookie2")
	}

	modifyResponse := func(resp *http.Response) error {
		// Handle 307/308 redirect if enabled
		redirectCount := 0
		previousURL := resp.Request.URL.String()
		for enableRedirect && (resp.StatusCode == 307 || resp.StatusCode == 308) {
			if redirectCount >= maxRedirects {
				return fmt.Errorf("maximum redirect limit (%d) reached", maxRedirects)
			}
			location := resp.Header.Get("Location")
			if location == "" {
				break
			}
			// Close the original response body
			resp.Body.Close()

			// Determine the redirect URL
			var redirectURL string
			if hasScheme(location) {
				// absolute URL with scheme, use it directly
				redirectURL = location
			} else {
				// relative URL, resolve it against the original request URL
				orig := resp.Request.URL
				base := &url.URL{
					Scheme: orig.Scheme,
					Host:   orig.Host,
				}
				u, err := url.Parse(location)
				if err != nil {
					return err
				}
				redirectURL = base.ResolveReference(u).String()
			}

			// If redirectURL is the same as previous, break and set status 400
			if redirectURL == previousURL {
				resp.StatusCode = 400
				resp.Status = "400 Bad Request"
				resp.Body = io.NopCloser(strings.NewReader(
					"redirect loop detected: redirect URL is the same as previous: " + redirectURL,
				))
				break
			}
			previousURL = redirectURL

			client := &http.Client{
				Timeout: timeout,
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}
			req, err := http.NewRequest(resp.Request.Method, redirectURL, nil)
			if err != nil {
				return err
			}
			// Copy headers from the original response to the new request
			for k, v := range resp.Request.Header {
				for _, vv := range v {
					req.Header.Add(k, vv)
				}
			}
			newResp, err := client.Do(req)
			if err != nil {
				return err
			}
			*resp = *newResp
			redirectCount++
		}

		resp.Header.Set("access-control-allow-origin", "*")
		resp.Header.Set("access-control-max-age", "3000000")
		return nil
	}

	proxy := &httputil.ReverseProxy{
		Director:       director,
		ModifyResponse: modifyResponse,
	}

	proxy.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   timeout,
			KeepAlive: timeout,
		}).Dial,
		TLSHandshakeTimeout: timeout,
		// TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corsURL := chi.URLParam(r, "*")
		if !hasScheme(corsURL) {
			corsURL = "http://" + corsURL
		}
		// Verify cors proxy url is valid
		_, err := url.Parse(corsURL)
		if err != nil {
			respondError(w, r, "invalid cors proxy url")
			return
		}

		// Handle pre-flight
		if r.Method == "OPTIONS" {
			handlePreflight(w, r)
			return
		}

		// Require origin header (if enabled)
		if requireOrigin && r.Header.Get("origin") == "" {
			respondError(w, r, "origin header is required on the request")
			return
		}

		proxy.ServeHTTP(w, r)
	})
}

func handlePreflight(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w, r)
	w.WriteHeader(200)
}

func respondError(w http.ResponseWriter, r *http.Request, body string) {
	setCORSHeaders(w, r)
	w.WriteHeader(422)
	w.Write([]byte(body))
}

func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("access-control-allow-origin", "*")
	w.Header().Set("access-control-max-age", "3000000")

	if r.Header.Get("access-control-request-method") != "" {
		w.Header().Set("access-control-allow-methods", r.Header.Get("access-control-request-method"))
	}

	if r.Header.Get("access-control-request-headers") != "" {
		w.Header().Set("access-control-request-headers", r.Header.Get("access-control-request-headers"))
	}
}

var corsAnywhereUsage = `cors-anywhere usage:

http://localhost:<port>/http(s)://your-domain.com/endpoint

Inspired by https://github.com/Redocly/cors-anywhere
`

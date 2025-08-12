package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"github.com/xakepp35/anygate/config"
	"github.com/xakepp35/anygate/plugin"
	"github.com/xakepp35/anygate/router"
)

// 🏗️ Register — чертёж памяти, где каждый путь знает свою судьбу.
func Register(r *router.Router, cfg config.Root, inheritedPlugins ...plugin.Spec) {
	log.Info().
		Int("routes", len(cfg.Routes)).
		Int("swagger", len(cfg.Swagger)).
		Int("plugins_inherited", len(inheritedPlugins)).
		Int("plugins_cfg", len(cfg.Plugins)).
		Msg("Register(): starting registration")

	// собираем цепочку плагинов
	fullChain := make([]plugin.Spec, 0, len(inheritedPlugins)+len(cfg.Plugins))
	fullChain = append(fullChain, inheritedPlugins...)
	fullChain = append(fullChain, cfg.Plugins...)

	log.Debug().
		Int("full_chain_len", len(fullChain)).
		Msg("Register(): plugin chain built")

	// обрабатываем маршруты
	for fromSpec, to := range cfg.Routes {
		log.Debug().
			Str("fromSpec", fromSpec).
			Str("to", to).
			Msg("Register(): processing route spec")

		methods, from := parseFromSpec(fromSpec)
		log.Debug().
			Strs("methods", methods).
			Str("from", from).
			Msg("Register(): parsed methods and path")

		base, mode := New(from, to, cfg)
		log.Debug().
			Str("from", from).
			Str("to", to).
			Str("mode", mode).
			Msg("Register(): base handler created")

		final := plugin.BuildChain(base, fullChain...)
		log.Debug().
			Str("from", from).
			Str("to", to).
			Msg("Register(): plugin chain applied to base handler")

		for _, method := range methods {
			registerRoute(r, method, from, final)
			log.Info().
				Str("from", from).
				Str("method", method).
				Str("to", to).
				Str("mode", mode).
				Msg("Register(): route registered")
		}
	}

	// рекурсивная регистрация для групп
	for _, child := range cfg.Groups {
		log.Info().
			Int("child_routes", len(child.Routes)).
			Int("child_swagger", len(child.Swagger)).
			Msg("Register(): entering child group")
		Register(r, child, fullChain...)
	}

	// обработка swagger
	if len(cfg.Swagger) > 0 {
		log.Info().
			Any("swagger", cfg.Swagger).
			Msg("Register(): calling registerSwaggerMultiUI")
		registerSwaggerMultiUI(r, cfg)
	} else {
		log.Warn().
			Msg("Register(): no Swagger configurations found")
	}

	log.Info().Msg("Register(): finished registration")
}

func New(from, to string, cfg config.Root) (fasthttp.RequestHandler, string) {
	log.Info().
		Str("from", from).
		Str("to", to).
		Msg("New(): selecting handler type")

	switch {
	case to == "":
		log.Info().
			Str("from", from).
			Str("mode", "ok").
			Msg("New(): matched empty target → Ok handler")
		return Ok, "ok"

	case to == "*":
		log.Info().
			Str("from", from).
			Str("mode", "echo").
			Msg("New(): matched '*' target → Echo handler")
		return Echo, "echo"

	case strings.HasPrefix(to, "http://") || strings.HasPrefix(to, "https://"):
		log.Info().
			Str("from", from).
			Str("target_url", to).
			Str("mode", "proxy").
			Msg("New(): matched HTTP/HTTPS → Proxy handler")
		h := NewProxy(from, to, cfg.Proxy)
		log.Debug().
			Str("from", from).
			Str("target_url", to).
			Msg("New(): Proxy handler created")
		return h, "proxy"

	case isFixedResponse(to):
		code, body := parseFixedResponse(to)
		log.Info().
			Str("from", from).
			Str("mode", "fixed").
			Int("status_code", code).
			Int("body_len", len(body)).
			Msg("New(): matched fixed response")
		return NewFixed(code, body), "fixed"

	default:
		log.Info().
			Str("from", from).
			Str("target_path", to).
			Str("mode", "static").
			Msg("New(): default → Static handler")
		return NewStatic(from, to, cfg.Static), "static"
	}
}

func parseFromSpec(spec string) ([]string, string) {
	parts := strings.Fields(spec)
	if len(parts) < 2 {
		return []string{"ANY"}, spec // если нет пробелов — просто путь
	}
	methods := make([]string, 0, len(parts)-1)
	for _, mstr := range parts[:len(parts)-1] {
		m := strings.ToUpper(mstr)
		if m == "*" || m == "ANY" {
			return []string{"ANY"}, parts[len(parts)-1]
		}
		methods = append(methods, strings.ToUpper(m))
	}
	return methods, parts[len(parts)-1]
}

/* ================= Helpers ================= */

type swaggerEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "spec"
	}
	return s
}

func pickExt(u string) string {
	l := strings.ToLower(u)
	if strings.HasSuffix(l, ".yaml") || strings.HasSuffix(l, ".yml") {
		return ".yaml"
	}
	return ".json"
}

// нормализуем «базу» (если передали /swagger/index.html, /swagger, /swagger/doc.json и т.п.)
func normalizeBaseURL(u string) (base string) {
	l := strings.ToLower(strings.TrimSpace(u))
	switch {
	case strings.HasSuffix(l, "/swagger/index.html"):
		return u[:len(u)-len("/swagger/index.html")]
	case strings.HasSuffix(l, "/swagger/doc.json"):
		return u[:len(u)-len("/swagger/doc.json")]
	case strings.HasSuffix(l, "/swagger/"):
		return u[:len(u)-len("/swagger/")]
	case strings.HasSuffix(l, "/swagger"):
		return u[:len(u)-len("/swagger")]
	default:
		return strings.TrimRight(u, "/")
	}
}

func resolveURL(baseStr, relStr string) (string, error) {
	bu, err := url.Parse(baseStr)
	if err != nil {
		return "", err
	}
	ru, err := url.Parse(relStr)
	if err != nil {
		return "", err
	}
	return bu.ResolveReference(ru).String(), nil
}

func httpGet(client *fasthttp.Client, u string) (int, []byte, error) {
	var req fasthttp.Request
	var resp fasthttp.Response
	req.SetRequestURI(u)
	req.Header.SetMethod(fasthttp.MethodGet)
	if err := client.Do(&req, &resp); err != nil {
		return 0, nil, err
	}
	return resp.StatusCode(), append([]byte(nil), resp.Body()...), nil
}

// форсим Accept, чтобы апстрим не отдал HTML вместо JSON/YAML
func withAcceptForSpecs(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Request.Header.Set("Accept", "application/json, application/yaml, application/x-yaml, text/yaml, */*;q=0.1")
		next(ctx)
	}
}

/* =========== Парсинг index.html =========== */

// A) urls: [ {name,url}, ... ] внутри SwaggerUIBundle({...})
func extractURLsBlockFromIndexHTML(html []byte) []byte {
	re := regexp.MustCompile(`urls\s*:\s*(\[[\s\S]*?\])`)
	m := re.FindSubmatch(html)
	if len(m) >= 2 {
		return bytes.TrimSpace(m[1])
	}
	return nil
}

func normalizeJSArrayToJSON(b []byte) []byte {
	s := string(b)
	// убрать комментарии
	reLine := regexp.MustCompile(`(?m)//.*$`)
	reBlock := regexp.MustCompile(`(?s)/\*.*?\*/`)
	s = reLine.ReplaceAllString(s, "")
	s = reBlock.ReplaceAllString(s, "")
	// ' -> "
	s = strings.ReplaceAll(s, `'`, `"`)
	// ключи без кавычек -> в кавычки
	reKey := regexp.MustCompile(`(\{|,)\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*:`)
	s = reKey.ReplaceAllString(s, `${1}"${2}":`)
	// висячие запятые
	reTrailing := regexp.MustCompile(`,(\s*[\]\}])`)
	s = reTrailing.ReplaceAllString(s, `$1`)
	return []byte(s)
}

// B) одиночный url: "..."
func extractSingleURLFromIndexHTML(html []byte) string {
	// поддержим и двойные, и одинарные кавычки
	re := regexp.MustCompile(`url\s*:\s*["']([^"']+)["']`)
	m := re.FindSubmatch(html)
	if len(m) >= 2 {
		return string(m[1])
	}
	return ""
}

// C) любые *.json|*.yaml|*.yml в href/src
func extractSpecLikeLinks(html []byte) []string {
	re := regexp.MustCompile(`(?:href|src)\s*=\s*["']([^"']+\.(?:json|ya?ml))["']`)
	out := []string{}
	seen := map[string]struct{}{}
	for _, m := range re.FindAllSubmatch(html, -1) {
		u := string(m[1])
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

/* ============== Main ============== */

func registerSwaggerMultiUI(r *router.Router, cfg config.Root) {
	log.Info().Msg("Registering Swagger UI handlers (index.html autodiscovery)")

	client := &fasthttp.Client{
		ReadTimeout:  6 * time.Second,
		WriteTimeout: 6 * time.Second,
	}

	urlsList := make([]string, 0, 16)

	for name, remote := range cfg.Swagger {
		baseName := name
		base := normalizeBaseURL(remote) // можно давать /swagger/index.html, /swagger, /swagger/doc.json, просто /host:port
		base = strings.TrimRight(base, "/")
		slug := slugify(baseName)

		indexURL := base + "/swagger/index.html"
		log.Info().Str("name", baseName).Str("indexURL", indexURL).Msg("Fetching swagger index.html")

		status, body, err := httpGet(client, indexURL)
		if err != nil || status >= 300 {
			log.Error().Err(err).Int("status", status).Str("indexURL", indexURL).Msg("Failed to fetch index.html; falling back to common paths")

			// Фолбэки (включая YAML)
			candidates := []string{
				base + "/swagger/doc.json",
				base + "/swagger.json",
				base + "/openapi.json",
				base + "/v3/api-docs",
				base + "/swagger/doc.yaml",
				base + "/swagger/doc.yml",
				base + "/openapi.yaml",
				base + "/openapi.yml",
			}
			for i, c := range candidates {
				specPath := fmt.Sprintf("/swagger/specs/%s-fallback-%d%s", slug, i, pickExt(c))
				proxyH, proxyType := New(specPath, c, cfg)
				if proxyType != "proxy" {
					continue
				}
				r.Register("GET", specPath, withAcceptForSpecs(proxyH))
				urlsList = append(urlsList, fmt.Sprintf(`{name: %q, url: %q}`, baseName+" • fallback", specPath))
				log.Info().Str("remote", c).Str("specPath", specPath).Msg("Registered fallback spec")
			}
			continue
		}

		found := 0
		// Для index.html swagger’а все относительные пути считаем относительно /swagger/
		relBase := base + "/swagger/"

		// A) urls: [...]
		if blk := extractURLsBlockFromIndexHTML(body); blk != nil {
			norm := normalizeJSArrayToJSON(blk)
			var entries []swaggerEntry
			if err := json.Unmarshal(norm, &entries); err == nil && len(entries) > 0 {
				log.Info().Int("count", len(entries)).Str("base", base).Msg("Found urls[] in index.html")
				for i, it := range entries {
					abs, err := resolveURL(relBase, it.URL)
					if err != nil {
						log.Error().Err(err).Str("rel", it.URL).Msg("resolve url failed")
						continue
					}
					specPath := "/swagger/specs/" + slug
					if it.Name != "" {
						specPath += "-" + slugify(it.Name)
					} else {
						specPath += fmt.Sprintf("-%d", i)
					}
					specPath += pickExt(abs)

					proxyH, proxyType := New(specPath, abs, cfg)
					if proxyType != "proxy" {
						continue
					}
					r.Register("GET", specPath, withAcceptForSpecs(proxyH))
					uiName := baseName
					if it.Name != "" {
						uiName += " • " + it.Name
					}
					urlsList = append(urlsList, fmt.Sprintf(`{name: %q, url: %q}`, uiName, specPath))
					found++
				}
			} else {
				log.Warn().Err(err).Msg("Failed to parse urls[] from index.html")
			}
		}

		// B) url: "doc.json" (твой случай)
		if found == 0 {
			if single := extractSingleURLFromIndexHTML(body); single != "" {
				if abs, err := resolveURL(relBase, single); err == nil {
					specPath := "/swagger/specs/" + slug + pickExt(abs)
					proxyH, proxyType := New(specPath, abs, cfg)
					if proxyType == "proxy" {
						r.Register("GET", specPath, withAcceptForSpecs(proxyH))
						urlsList = append(urlsList, fmt.Sprintf(`{name: %q, url: %q}`, baseName, specPath))
						log.Info().Str("remote", abs).Str("specPath", specPath).Msg("Registered single spec from index.html")
						found++
					}
				}
			}
		}

		// C) ссылочные *.json/*.yaml/*.yml
		if found == 0 {
			links := extractSpecLikeLinks(body)
			if len(links) > 0 {
				log.Info().Int("count", len(links)).Msg("Found spec-like links in index.html")
				for i, rel := range links {
					abs, err := resolveURL(relBase, rel)
					if err != nil {
						continue
					}
					specPath := fmt.Sprintf("/swagger/specs/%s-link-%d%s", slug, i, pickExt(abs))
					proxyH, proxyType := New(specPath, abs, cfg)
					if proxyType != "proxy" {
						continue
					}
					r.Register("GET", specPath, withAcceptForSpecs(proxyH))
					urlsList = append(urlsList, fmt.Sprintf(`{name: %q, url: %q}`, baseName+" • link", specPath))
					found++
				}
			}
		}

		// D) если вообще ничего — фолбэки
		if found == 0 {
			log.Warn().Str("base", base).Msg("No specs found in index.html; applying fallbacks")
			candidates := []string{
				base + "/swagger/doc.json",
				base + "/swagger.json",
				base + "/openapi.json",
				base + "/v3/api-docs",
				base + "/swagger/doc.yaml",
				base + "/swagger/doc.yml",
				base + "/openapi.yaml",
				base + "/openapi.yml",
			}
			for i, c := range candidates {
				specPath := fmt.Sprintf("/swagger/specs/%s-fallback-%d%s", slug, i, pickExt(c))
				proxyH, proxyType := New(specPath, c, cfg)
				if proxyType != "proxy" {
					continue
				}
				r.Register("GET", specPath, withAcceptForSpecs(proxyH))
				urlsList = append(urlsList, fmt.Sprintf(`{name: %q, url: %q}`, baseName+" • fallback", specPath))
			}
		}
	}

	// UI
	html := fmt.Sprintf(`<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <title>Swagger UI</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script>
      window.ui = SwaggerUIBundle({
        urls: [%s],
        dom_id: '#swagger-ui',
        deepLinking: true
      });
    </script>
  </body>
</html>`, strings.Join(urlsList, ",\n        "))

	handler := func(ctx *fasthttp.RequestCtx) {
		log.Info().Str("path", string(ctx.Path())).Int("specs", len(urlsList)).Msg("Serving Swagger UI")
		ctx.SetContentType("text/html; charset=utf-8")
		ctx.SetStatusCode(fasthttp.StatusOK)
		_, _ = ctx.WriteString(html)
	}
	r.Register("GET", "/swagger", handler)
	r.Register("GET", "/swagger/index.html", handler)
	log.Info().Int("specs_in_ui", len(urlsList)).Msg("Swagger UI handlers registered (index.html autodiscovery)")
}

// func parseFromSpec(to string) (method, path string) {
// 	for i := 0; i < len(to); i++ {
// 		if to[i] == ' ' {
// 			return strings.ToUpper(to[:i]), to[i+1:]
// 		}
// 	}
// 	return "ANY", to
// }

func registerRoute(r *router.Router, method, path string, h fasthttp.RequestHandler) {
	r.Register(method, path, h)
}

// func registerRoute(r *router.Router, method, path string, h fasthttp.RequestHandler) {
// 	switch method {
// 	case "ANY":
// 		r.ANY(path, h)
// 	case "GET":
// 		r.GET(path, h)
// 	case "HEAD":
// 		r.HEAD(path, h)
// 	case "POST":
// 		r.POST(path, h)
// 	case "PUT":
// 		r.PUT(path, h)
// 	case "PATCH":
// 		r.PATCH(path, h)
// 	case "DELETE":
// 		r.DELETE(path, h)
// 	case "CONNECT":
// 		r.CONNECT(path, h)
// 	case "OPTIONS":
// 		r.OPTIONS(path, h)
// 	case "TRACE":
// 		r.TRACE(path, h)
// 	default:
// 		log.Fatal().Str("method", method).Str("path", path).Msg("unsupported method")
// 	}
// }

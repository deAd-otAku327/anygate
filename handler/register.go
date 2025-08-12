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

func isBaseSwaggerURL(u string) bool {
	l := strings.ToLower(strings.TrimSpace(u))
	return !(strings.HasSuffix(l, ".json") || strings.HasSuffix(l, ".yaml") || strings.HasSuffix(l, ".yml"))
}

type swaggerEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func discoverSwaggerURLs(base string) ([]swaggerEntry, error) {
	initializerURL := strings.TrimRight(base, "/") + "/swagger-initializer.js"

	cli := &fasthttp.Client{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	var req fasthttp.Request
	var resp fasthttp.Response
	req.SetRequestURI(initializerURL)
	req.Header.SetMethod(fasthttp.MethodGet)

	if err := cli.Do(&req, &resp); err != nil {
		return nil, fmt.Errorf("fetch initializer: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return nil, fmt.Errorf("initializer status %d", resp.StatusCode())
	}

	body := resp.Body()

	// вытащим блок urls: [ ... ]
	re := regexp.MustCompile(`urls\s*:\s*(\[[\s\S]*?\])`)
	m := re.FindSubmatch(body)
	if len(m) < 2 {
		return nil, fmt.Errorf("urls array not found in initializer")
	}

	raw := bytes.TrimSpace(m[1])
	norm := normalizeJSArrayToJSON(raw)

	var out []swaggerEntry
	if err := json.Unmarshal(norm, &out); err != nil {
		log.Error().Err(err).
			RawJSON("normalized_urls_block", norm).
			Msg("parse initializer urls failed")
		return nil, fmt.Errorf("parse urls json: %w", err)
	}
	return out, nil
}

// Приводим JS-массив объектов к валидному JSON
func normalizeJSArrayToJSON(b []byte) []byte {
	s := string(b)

	// убрать комментарии
	reLineComment := regexp.MustCompile(`(?m)//.*$`)
	s = reLineComment.ReplaceAllString(s, "")
	reBlockComment := regexp.MustCompile(`(?s)/\*.*?\*/`)
	s = reBlockComment.ReplaceAllString(s, "")

	// ' -> "
	s = strings.ReplaceAll(s, `'`, `"`)

	// ключи без кавычек -> в кавычки
	reKey := regexp.MustCompile(`(\{|,)\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*:`)
	s = reKey.ReplaceAllString(s, `${1}"${2}":`)

	// убрать висячие запятые
	reTrailingComma := regexp.MustCompile(`,(\s*[\]\}])`)
	s = reTrailingComma.ReplaceAllString(s, `$1`)

	return []byte(s)
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
	if ru.IsAbs() {
		return ru.String(), nil
	}
	return bu.ResolveReference(ru).String(), nil
}

func pickExt(u string) string {
	l := strings.ToLower(u)
	if strings.HasSuffix(l, ".yaml") || strings.HasSuffix(l, ".yml") {
		return ".yaml"
	}
	return ".json"
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

// --- main ---

func registerSwaggerMultiUI(r *router.Router, cfg config.Root) {
	log.Info().Msg("Registering Swagger UI handlers (auto-discovery enabled)")
	urlsList := make([]string, 0, 16)

	for name, remote := range cfg.Swagger {
		baseName := name
		baseURL := strings.TrimSpace(remote)
		slug := slugify(baseName)

		if isBaseSwaggerURL(baseURL) {
			log.Info().Str("name", baseName).Str("base", baseURL).Msg("Swagger autodiscover: fetching swagger-initializer.js")

			discovered, err := discoverSwaggerURLs(baseURL)
			if err != nil {
				log.Error().Err(err).Str("base", baseURL).Str("name", baseName).Msg("Swagger autodiscover failed")
			} else {
				log.Info().Int("count", len(discovered)).Str("base", baseURL).Msg("Swagger autodiscover: urls found")
				for i, it := range discovered {
					absURL, err := resolveURL(baseURL, it.URL)
					if err != nil {
						log.Error().Err(err).Str("base", baseURL).Str("url", it.URL).Msg("resolve url failed")
						continue
					}
					specPath := "/swagger/specs/" + slug
					if it.Name != "" {
						specPath += "-" + slugify(it.Name)
					} else {
						specPath += fmt.Sprintf("-%d", i)
					}
					specPath += pickExt(absURL)

					log.Info().Str("name", baseName).Str("remote", absURL).Str("specPath", specPath).Msg("Registering discovered spec (proxy)")
					proxyH, proxyType := New(specPath, absURL, cfg)
					if proxyType != "proxy" {
						log.Fatal().Str("remote", absURL).Str("type", proxyType).Msg("Expected proxy handler for discovered spec")
					}
					r.Register("GET", specPath, proxyH)

					uiName := baseName
					if it.Name != "" {
						uiName = baseName + " • " + it.Name
					}
					urlsList = append(urlsList, fmt.Sprintf(`{name: %q, url: %q}`, uiName, specPath))
				}
			}

			// если ничего не нашли — подкинем фолбэки самых частых путей
			if len(urlsList) == 0 {
				log.Warn().Str("base", baseURL).Str("name", baseName).Msg("Swagger autodiscover produced no entries; applying fallbacks")
				candidates := []string{
					strings.TrimRight(baseURL, "/") + "/swagger/doc.json",
					strings.TrimRight(baseURL, "/") + "/swagger.json",
					strings.TrimRight(baseURL, "/") + "/v3/api-docs",
					strings.TrimRight(baseURL, "/") + "/openapi.json",
				}
				for i, c := range candidates {
					specPath := fmt.Sprintf("/swagger/specs/%s-fallback-%d.json", slug, i)
					proxyH, proxyType := New(specPath, c, cfg)
					if proxyType != "proxy" {
						continue
					}
					r.Register("GET", specPath, proxyH)
					urlsList = append(urlsList, fmt.Sprintf(`{name: %q, url: %q}`, baseName+" • fallback", specPath))
					log.Info().Str("base", baseURL).Str("remote", c).Str("specPath", specPath).Msg("Registered fallback spec")
				}
			}

			continue
		}

		// прямая ссылка на .json/.yaml
		ext := ".yaml"
		if strings.HasSuffix(strings.ToLower(baseURL), ".json") {
			ext = ".json"
		}
		specPath := "/swagger/specs/" + slug + ext

		log.Info().Str("name", baseName).Str("remote", baseURL).Str("specPath", specPath).Msg("Registering single spec (proxy)")
		proxyH, proxyType := New(specPath, baseURL, cfg)
		if proxyType != "proxy" {
			log.Fatal().Str("remote", baseURL).Str("type", proxyType).Msg("Expected proxy handler (single spec)")
		}
		r.Register("GET", specPath, proxyH)
		urlsList = append(urlsList, fmt.Sprintf(`{name: %q, url: %q}`, baseName, specPath))
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
		log.Info().Str("path", string(ctx.Path())).Msg("Serving Swagger UI")
		ctx.SetContentType("text/html; charset=utf-8")
		ctx.SetStatusCode(fasthttp.StatusOK)
		_, _ = ctx.WriteString(html)
	}
	r.Register("GET", "/swagger", handler)
	r.Register("GET", "/swagger/index.html", handler)

	log.Info().Int("specs_in_ui", len(urlsList)).Msg("Swagger UI handlers registered")
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

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

// --- ХЕЛПЕРЫ ---

// распознать базовый адрес сервиса vs прямая ссылка на спецификацию
func isBaseSwaggerURL(u string) bool {
	l := strings.ToLower(u)
	return !(strings.HasSuffix(l, ".json") || strings.HasSuffix(l, ".yaml") || strings.HasSuffix(l, ".yml"))
}

// получить swagger-initializer.js и вытащить из него массив urls
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

	// Находим кусок `urls: [ ... ]` (допускаем пробелы/переносы)
	re := regexp.MustCompile(`urls\s*:\s*(\[[\s\S]*?\])`)
	m := re.FindSubmatch(body)
	if len(m) < 2 {
		return nil, fmt.Errorf("urls array not found in initializer")
	}

	raw := bytes.TrimSpace(m[1])

	// В swagger-initializer.js обычно JS-объекты, а не строгое JSON.
	// Попробуем быстрый «почин»: заменим одинарные кавычки и безкавычный name/url на JSON-валидный формат.
	norm := normalizeJSArrayToJSON(raw)

	var out []swaggerEntry
	if err := json.Unmarshal(norm, &out); err != nil {
		// на всякий случай логируем, чтобы было видно на что упали
		log.Error().Err(err).RawJSON("raw_urls_block", norm).Msg("parse initializer urls failed")
		return nil, fmt.Errorf("parse urls json: %w", err)
	}
	return out, nil
}

type swaggerEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Пытаемся привести JS-массив объектов к JSON
func normalizeJSArrayToJSON(b []byte) []byte {
	s := string(b)
	// 1) кавычки одинарные -> двойные
	s = strings.ReplaceAll(s, `'`, `"`)

	// 2) ключи без кавычек -> в кавычки (только name и url нам важны)
	//   {name: "X", url: "/a"} -> {"name":"X","url":"/a"}
	reKey := regexp.MustCompile(`(\{|,)\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*:`)
	s = reKey.ReplaceAllString(s, `${1}"${2}":`)

	return []byte(s)
}

// правильная склейка base и относительного пути (./, ../, /)
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
	// относительный — резолвим относительно base
	return bu.ResolveReference(ru).String(), nil
}

// подобрать расширение по URL
func pickExt(u string) string {
	l := strings.ToLower(u)
	switch {
	case strings.HasSuffix(l, ".yaml"), strings.HasSuffix(l, ".yml"):
		return ".yaml"
	default:
		return ".json"
	}
}

// «слаг» из имени (в нижнем регистре, только a-z0-9-_)
func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimSpace(s)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "spec"
	}
	return s
}

// --- ОСНОВНАЯ ФУНКЦИЯ ---

func registerSwaggerMultiUI(r *router.Router, cfg config.Root) {
	log.Info().Msg("Registering Swagger UI handlers (auto-discovery enabled)")
	urlsList := make([]string, 0, 16)

	for name, remote := range cfg.Swagger {
		slug := slugify(name)

		if isBaseSwaggerURL(remote) {
			// 1) autodiscover из swagger-initializer.js
			discovered, err := discoverSwaggerURLs(remote)
			if err != nil {
				log.Error().Err(err).Str("base", remote).Str("name", name).Msg("Swagger autodiscover failed")
				continue
			}
			if len(discovered) == 0 {
				log.Warn().Str("base", remote).Str("name", name).Msg("Swagger autodiscover: no urls found")
				continue
			}

			for i, it := range discovered {
				// резолвим относительные пути в абсолютные
				absURL, err := resolveURL(remote, it.URL)
				if err != nil {
					log.Error().Err(err).Str("base", remote).Str("url", it.URL).Msg("resolve url failed")
					continue
				}
				// делаем прокси-путь у нас
				specPath := "/swagger/specs/" + slug
				if it.Name != "" {
					specPath += "-" + slugify(it.Name)
				} else {
					specPath += fmt.Sprintf("-%d", i)
				}
				specPath += pickExt(absURL)

				// регистрируем прокси
				log.Info().Str("name", name).Str("remote", absURL).Str("specPath", specPath).Msg("Registering proxied discovered spec")
				proxyH, proxyType := New(specPath, absURL, cfg)
				if proxyType != "proxy" {
					log.Fatal().Str("remote", absURL).Str("type", proxyType).Msg("Expected proxy handler for discovered spec")
				}
				r.Register("GET", specPath, proxyH)

				// добавляем в список для UI
				uiName := name
				if it.Name != "" {
					uiName = name + " • " + it.Name
				}
				urlsList = append(urlsList, fmt.Sprintf(`{name: %q, url: %q}`, uiName, specPath))
			}

			continue
		}

		// 2) классический режим: remote — это конкретный .json/.yaml
		ext := ".yaml"
		if strings.HasSuffix(strings.ToLower(remote), ".json") {
			ext = ".json"
		}
		specPath := "/swagger/specs/" + slug + ext

		log.Info().Str("name", name).Str("remote", remote).Str("specPath", specPath).Msg("Registering proxy handler (single spec)")
		proxyH, proxyType := New(specPath, remote, cfg)
		if proxyType != "proxy" {
			log.Fatal().Str("remote", remote).Str("type", proxyType).Msg("Expected proxy handler (single spec)")
		}
		r.Register("GET", specPath, proxyH)
		urlsList = append(urlsList, fmt.Sprintf(`{name: %q, url: %q}`, name, specPath))
	}

	// 3) сам UI
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
	// пути UI
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

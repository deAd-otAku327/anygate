package handler

import (
	"fmt"
	"strings"

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

func registerSwaggerMultiUI(r *router.Router, cfg config.Root) {
	log.Info().Msg("Registering Swagger UI handlers")
	urlsList := make([]string, 0, len(cfg.Swagger))

	for name, remote := range cfg.Swagger {
		slug := strings.ToLower(name)
		ext := ".yaml"
		if strings.HasSuffix(remote, ".json") {
			ext = ".json"
		}

		specPath := "/swagger/specs/" + slug + ext
		log.Info().Str("name", name).Str("remote", remote).Str("specPath", specPath).Msg("Registering proxy handler")
		proxyH, proxyType := New(specPath, remote, cfg)
		if proxyType != "proxy" {
			log.Fatal().Str("remote", remote).Str("type", proxyType).Msg("Expected proxy handler")
		}
		r.Register("GET", specPath, proxyH)
		urlsList = append(urlsList, fmt.Sprintf(`{name: %q, url: %q}`, name, specPath))
	}

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
	log.Info().Msg("Swagger UI handlers registered")
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

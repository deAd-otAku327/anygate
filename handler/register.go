package handler

import (
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
			RegisterRoute(r, method, from, final)
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
		RegisterSwaggerMultiUI(r, cfg)
	} else {
		log.Warn().
			Msg("Register(): no Swagger configurations found")
	}

	log.Info().Msg("Register(): finished registration")
}

// Создание хендлера.
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

	case strings.HasPrefix(to, "http://") || strings.HasPrefix(to, "https://") || strings.HasPrefix(to, "ws://") || strings.HasPrefix(to, "wss://"):
		log.Info().
			Str("from", from).
			Str("target_url", to).
			Str("mode", "proxy").
			Msg("New(): matched HTTP/HTTPS/WS/WSS → Proxy handler")
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

func RegisterRoute(r *router.Router, method, path string, h fasthttp.RequestHandler) {
	r.Register(method, path, h)
}

func HTTPGet(client *fasthttp.Client, u string) (int, []byte, error) {
	var req fasthttp.Request
	var resp fasthttp.Response
	req.SetRequestURI(u)
	req.Header.SetMethod(fasthttp.MethodGet)
	if err := client.Do(&req, &resp); err != nil {
		return 0, nil, err
	}
	return resp.StatusCode(), resp.Body(), nil
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

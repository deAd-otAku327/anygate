package handler

import (
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"github.com/xakepp35/anygate/config"
	"github.com/xakepp35/anygate/utils"
)

// 🚀 NewProxy — минималистичный шприц, что вшивает внешний мир в твой процесс, избегая копий, но сохраняя смысл.
func NewProxy(from, to string, cfg config.Proxy) fasthttp.RequestHandler {
	if cfg.StatusBadGateway == 0 {
		cfg.StatusBadGateway = fasthttp.StatusBadGateway
	}
	if cfg.StatusGatewayTimeout == 0 {
		cfg.StatusGatewayTimeout = fasthttp.StatusGatewayTimeout
	}
	if cfg.RouteLenHint == 0 {
		cfg.RouteLenHint = 64
	}
	pathBuilder := utils.NewPathBuilder(to, len(from), cfg.RouteLenHint)
	client := NewClient(cfg)
	return func(ctx *fasthttp.RequestCtx) {
		uri := pathBuilder.Build(ctx.Path())
		ctx.Request.SetRequestURI(uri)
		ctx.Request.SetTimeout(cfg.Timeout)
		log.Info().
			Str("from", from).
			Str("to", to).
			Str("uri", uri).
			Str("method", string(ctx.Method())).
			Str("path", string(ctx.Path())).
			Msg("Proxying request")
		err := client.Do(&ctx.Request, &ctx.Response)
		switch err {
		case nil:
			log.Info().
				Str("to", to).
				Int("status", ctx.Response.StatusCode()).
				Msg("Proxy success")
		case fasthttp.ErrTimeout:
			ctx.SetStatusCode(cfg.StatusGatewayTimeout)
			ctx.SetBodyString(`{"error":"timeout"}`)
			log.Error().
				Err(err).
				Str("to", to).
				Str("from", from).
				Msg("timeout")
		default:
			ctx.SetStatusCode(cfg.StatusBadGateway)
			ctx.SetBodyString(`{"error":"` + err.Error() + `"}`)
			log.Error().
				Err(err).
				Str("to", to).
				Str("from", from).
				Msg("gateway")
		}
	}
}

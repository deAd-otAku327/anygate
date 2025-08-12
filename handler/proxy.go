package handler

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"github.com/xakepp35/anygate/config"
	"github.com/xakepp35/anygate/utils"
)

// 🚀 NewProxy — проксирует с сохранением raw query и без мутации исходного ctx.Request
func NewProxy(from, to string, cfg config.Proxy) fasthttp.RequestHandler {
	log.Info().Msg("IM HEREREERERRERE")

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
		// 1) Собираем базовую цель (host+scheme+path) через твой builder
		target := pathBuilder.Build(ctx.Path())

		fmt.Println("1111")
		fmt.Println(ctx.URI().QueryString())
		fmt.Println("2222")

		// 2) Приклеиваем СЫРОЙ query из входящего запроса (без переэнкодинга)
		if raw := ctx.URI().QueryString(); len(raw) > 0 {
			if strings.Contains(target, "?") {
				target += "&" + string(raw)
			} else {
				target += "?" + string(raw)
			}
		}

		// 3) Готовим отдельный запрос (не трогаем ctx.Request)
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()
		defer func() {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
		}()

		ctx.Request.CopyTo(req)     // копируем метод/заголовки/тело
		req.SetRequestURI(target)   // абсолютный URI с нашим query
		req.SetTimeout(cfg.Timeout) // если задан

		// Проставим корректный Host для апстрима (на всякий случай)
		if h := req.URI().Host(); len(h) > 0 {
			req.Header.SetHostBytes(h)
		}

		log.Info().
			Str("from", from).
			Str("to", to).
			Str("method", string(req.Header.Method())).
			Str("target", target).
			Bytes("raw_query_in", ctx.URI().QueryString()).
			Msg("proxy -> upstream")

		// 4) Шлём запрос и кладём ответ прямо в ctx.Response
		if err := client.Do(req, resp); err != nil {
			if err == fasthttp.ErrTimeout {
				ctx.SetStatusCode(cfg.StatusGatewayTimeout)
				ctx.SetBodyString(`{"error":"timeout"}`)
				log.Error().Err(err).Str("to", to).Str("from", from).Msg("timeout")
				return
			}
			ctx.SetStatusCode(cfg.StatusBadGateway)
			ctx.SetBodyString(`{"error":"` + err.Error() + `"}`)
			log.Error().Err(err).Str("to", to).Str("from", from).Msg("gateway")
			return
		}

		// Копируем ответ
		ctx.Response.SetStatusCode(resp.StatusCode())
		resp.Header.CopyTo(&ctx.Response.Header)
		ctx.Response.SetBodyRaw(resp.Body()) // zero-copy set

		log.Info().
			Str("to", to).
			Int("status", resp.StatusCode()).
			Msg("Proxy success")
	}
}

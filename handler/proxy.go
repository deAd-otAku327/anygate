package handler

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"github.com/xakepp35/anygate/config"
	"github.com/xakepp35/anygate/utils"
)

type connParams struct {
	targetURL *url.URL
	proxyFrom string
	proxyTo   string

	timeout              time.Duration
	statusBadGateway     int
	statusGatewayTimeout int
}

// 🚀 NewProxy — проксирует с сохранением raw query и без мутации исходного ctx.Request
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
	connParams := connParams{
		proxyFrom: from,
		proxyTo:   to,

		timeout:              cfg.Timeout,
		statusBadGateway:     cfg.StatusBadGateway,
		statusGatewayTimeout: cfg.StatusGatewayTimeout,
	}

	return func(ctx *fasthttp.RequestCtx) {
		// 1) Собираем базовую цель (scheme+host+path) через builder
		target := pathBuilder.Build(ctx.Path())

		// 2) Приклеиваем СЫРОЙ query из входящего запроса (без переэнкодинга)
		if raw := ctx.URI().QueryString(); len(raw) > 0 {
			if strings.Contains(target, "?") {
				target += "&" + string(raw)
			} else {
				target += "?" + string(raw)
			}
		}

		targetURL, err := url.Parse(target)
		if err != nil {
			ctx.Error("Internal server error", fasthttp.StatusInternalServerError)
			log.Error().
				Err(err).
				Str("rawURL", target).
				Msg("URL parsing failed")
			return
		}

		connParams.targetURL = targetURL

		if string(ctx.Method()) == "GET" && websocket.FastHTTPIsWebSocketUpgrade(ctx) {
			handleWebSocketConnection(ctx, &connParams)
			return
		}

		handleHTTPConnection(ctx, client, &connParams)
	}
}

func handleHTTPConnection(ctx *fasthttp.RequestCtx, client *fasthttp.Client, cp *connParams) {
	// 3) Готовим отдельный запрос (не трогаем ctx.Request)
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer func() {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
	}()

	ctx.Request.CopyTo(req)                  // копируем метод/заголовки/тело
	req.SetRequestURI(cp.targetURL.String()) // абсолютный URI с нашим query
	req.SetTimeout(cp.timeout)               // если задан

	// Проставим корректный Host для апстрима (на всякий случай)
	if h := req.URI().Host(); len(h) > 0 {
		req.Header.SetHostBytes(h)
	}

	log.Info().
		Str("from", cp.proxyFrom).
		Str("to", cp.proxyTo).
		Str("method", string(req.Header.Method())).
		Str("target", cp.targetURL.String()).
		Bytes("raw_query_in", ctx.URI().QueryString()).
		Msg("proxy -> upstream")

	// 4) Шлём запрос и кладём ответ прямо в ctx.Response
	if err := client.Do(req, resp); err != nil {
		if err == fasthttp.ErrTimeout {
			ctx.SetStatusCode(cp.statusGatewayTimeout)
			ctx.SetBodyString(`{"error":"timeout"}`)
			log.Error().Err(err).Str("from", cp.proxyFrom).Str("to", cp.proxyTo).Msg("timeout")
			return
		}
		ctx.SetStatusCode(cp.statusBadGateway)
		ctx.SetBodyString(`{"error":"` + err.Error() + `"}`)
		log.Error().Err(err).Str("from", cp.proxyFrom).Str("to", cp.proxyTo).Msg("gateway")
		return
	}

	// Копируем ответ
	ctx.Response.SetStatusCode(resp.StatusCode())
	resp.Header.CopyTo(&ctx.Response.Header)
	ctx.Response.SetBodyRaw(resp.Body()) // zero-copy set

	log.Info().
		Str("to", cp.proxyTo).
		Int("status", resp.StatusCode()).
		Msg("Proxy success")
}

func handleWebSocketConnection(ctx *fasthttp.RequestCtx, cp *connParams) {
	cp.targetURL.Scheme = "ws"
	fmt.Println("socket on " + cp.targetURL.String())

	serverConn, _, err := websocket.DefaultDialer.Dial(
		cp.targetURL.String(),
		nil,
	)
	if err != nil {
		log.Printf("Error connecting to server WebSocket on %s: %v", cp.targetURL, err)
		ctx.Error("Failed to connect to server", fasthttp.StatusBadGateway)
		return
	}

	connUpgrader := websocket.FastHTTPUpgrader{
		CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
			return true
		},
	}

	// Обновляем соединение клиента до WebSocket
	err = connUpgrader.Upgrade(ctx, func(clientConn *websocket.Conn) {

		defer func() {
			serverConn.Close()
			clientConn.Close()
		}()

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			forwardWebSocket(ctx, clientConn, serverConn, "client->server")
		}()

		go func() {
			defer wg.Done()
			forwardWebSocket(ctx, serverConn, clientConn, "server->client")
		}()

		wg.Wait()
		fmt.Printf("CALLBACK END for %s", clientConn.RemoteAddr())
	})

	if err != nil {
		fmt.Printf("WebSocket upgrade error: %v", err)
	}

	fmt.Printf("HANDLER END for %s", ctx.RemoteAddr())
}

func forwardWebSocket(ctx *fasthttp.RequestCtx, src, dst *websocket.Conn, direction string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			messageType, message, err := src.ReadMessage()
			fmt.Println("re ", string(message))
			if err != nil {
				fmt.Printf("WebSocket read error (%s): %v", direction, err)
				return
			}

			err = dst.WriteMessage(messageType, message)
			fmt.Println("wr ", string(message))
			if err != nil {
				fmt.Printf("WebSocket write error (%s): %v", direction, err)
				return
			}
		}
	}

}

package handler

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"github.com/xakepp35/anygate/config"
	"github.com/xakepp35/anygate/router"
)

type swaggerEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func RegisterSwaggerMultiUI(r *router.Router, cfg config.Root) {
	log.Info().Msg("Registering Swagger UI handlers")

	client := &fasthttp.Client{
		ReadTimeout:  6 * time.Second,
		WriteTimeout: 6 * time.Second,
	}

	entryList := make([]swaggerEntry, 0, 16)

	for name, remote := range cfg.Swagger {
		slug := slugify(name)

		// сначала пробуем достать спеку по исходному url
		log.Info().Msg("Trying to get spec from original url")
		status, body, err := HTTPGet(client, remote)
		if err == nil && status < 300 {
			// если вернулся ответ по исходному url, проверяем тело на наличие меток документации swagger (openapi)
			if bytes.Contains(body, []byte("swagger")) || bytes.Contains(body, []byte("openapi")) {
				specPath := fmt.Sprintf("/swagger/specs/%s", slug)
				proxyH, proxyType := New(specPath, remote, cfg)
				if proxyType != "proxy" {
					continue
				}

				RegisterRoute(r, "GET", specPath, wrapSpecProxy(withAcceptForSpecs(proxyH)))
				entryList = append(entryList, swaggerEntry{
					Name: name,
					URL:  specPath,
				})

				continue
			}
		}

		// если исходный url не выдал спеку - нормализуем и проверяем фолбеки (возможные ручки спеки)
		log.Info().Int("status", status).Msg("Failed to get spec from original url, applying fallbacks")

		base := normalizeBaseURL(remote)

		targetID, targetURL := findActiveFallbackRoute(client, base)
		if targetID < 0 {
			continue
		}

		specPath := fmt.Sprintf("/swagger/specs/%s-fallback-%d%s", slug, targetID, pickExt(targetURL))
		proxyH, proxyType := New(specPath, targetURL, cfg)
		if proxyType != "proxy" {
			continue
		}

		RegisterRoute(r, "GET", specPath, wrapSpecProxy(withAcceptForSpecs(proxyH)))
		entryList = append(entryList, swaggerEntry{
			Name: name + " • fallback",
			URL:  specPath,
		})
	}

	// далее: сборка мультиинтерфейса Swagger

	// если одна спека — отдадим через `url:` (UI сразу поднимет её)
	var singleURL, primaryName string
	if len(entryList) == 1 {
		singleURL = entryList[0].URL
	} else if len(entryList) > 1 {
		// возьмём имя первой спеки для "urls.primaryName"
		primaryName = entryList[0].Name
	}

	uiConfig := ""

	if singleURL != "" {
		uiConfig = fmt.Sprintf(`url: %q,`, singleURL)
	} else {
		// ВАЖНО: правильный ключ с точкой — "urls.primaryName"
		uiConfig = fmt.Sprintf(`urls: [%s],
        "urls.primaryName": %q,`,
			joinSwaggerEntries(entryList),
			primaryName,
		)
	}
	log.Info().Str("uiConfig", uiConfig).Msg("Applying ui config")

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
	<script src="https://unpkg.com/swagger-ui-dist@5.11.0/swagger-ui-standalone-preset.js"></script>
    <script>
      window.ui = SwaggerUIBundle({
        %s
        dom_id: '#swagger-ui',
        deepLinking: true,
        validatorUrl: null,
		presets: [
			SwaggerUIBundle.presets.apis,
			SwaggerUIStandalonePreset
		],
		layout: "StandaloneLayout",
      });
    </script>
  </body>
</html>`, uiConfig)

	handler := func(ctx *fasthttp.RequestCtx) {
		log.Info().Str("path", string(ctx.Path())).Int("specs", len(entryList)).Msg("Serving Swagger UI")
		ctx.SetContentType("text/html; charset=utf-8")
		ctx.SetStatusCode(fasthttp.StatusOK)
		_, _ = ctx.WriteString(html)
	}
	r.Register("GET", "/swagger", handler)
	r.Register("GET", "/swagger/index.html", handler)
	log.Info().Int("specs_in_ui", len(entryList)).Msg("Swagger UI handlers registered")
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

// Нормализуем «базу»: обрабатывает url на swagger (openapi), либо просто хост
func normalizeBaseURL(u string) string {
	lowered := strings.ToLower(strings.TrimSpace(u))

	norm, _, found := strings.Cut(lowered, "/swagger")
	if found {
		return norm
	}

	norm, _, found = strings.Cut(lowered, "/openapi")
	if found {
		return norm
	}

	return strings.TrimRight(lowered, "/")
}

// Форсим Accept (иначе некоторые сервисы отдают text/html)
func withAcceptForSpecs(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Request.Header.Set("Accept", "application/json, application/yaml, application/x-yaml, text/yaml, */*;q=0.1")
		next(ctx)
	}
}

// После проксирования чиним Content-Type, если апстрим отдал HTML/пусто
func wrapSpecProxy(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		next(ctx) // ответ уже в ctx.Response

		ct := string(ctx.Response.Header.ContentType())
		body := ctx.Response.Body()

		needsFix := ct == "" || strings.Contains(ct, "text/html")
		if needsFix && len(body) > 0 {
			switch body[0] {
			case '{', '[':
				ctx.Response.Header.SetContentType("application/json")
			default:
				trim := bytes.TrimSpace(body)
				if bytes.HasPrefix(trim, []byte("---")) ||
					bytes.HasPrefix(trim, []byte("openapi:")) ||
					bytes.HasPrefix(trim, []byte("swagger:")) {
					ctx.Response.Header.SetContentType("application/yaml")
				}
			}
		}

		log.Info().
			Int("status", ctx.Response.StatusCode()).
			Str("content_type", string(ctx.Response.Header.ContentType())).
			Str("path", string(ctx.Path())).
			Msg("spec proxy response")
	}
}

// Поиск первой активной у сервиса ручки сваггера из списка.
func findActiveFallbackRoute(client *fasthttp.Client, base string) (int, string) {
	candidates := []string{
		base + "/doc.json",
		base + "/swagger/doc.json",
		base + "/swagger.json",
		base + "/swagger.yaml",
		base + "/swagger.yml",
		base + "/swagger/swagger.json",
		base + "/swagger/swagger.yaml",
		base + "/swagger/swagger.yml",
		base + "/openapi.json",
		base + "/v3/api-docs",
		base + "/swagger/doc.yaml",
		base + "/swagger/doc.yml",
		base + "/openapi.yaml",
		base + "/openapi.yml",
	}

	for i, c := range candidates {
		status, _, err := HTTPGet(client, c)
		if err == nil && status == 200 {
			log.Info().Str("url", c).Msg("Found active fallback swagger route")
			return i, c
		}
	}

	return -1, ""
}

func joinSwaggerEntries(entries []swaggerEntry) string {
	result := ""
	for _, e := range entries {
		elem := fmt.Sprintf("{name: %q, url: %q}, ", e.Name, e.URL)
		result += elem
	}

	return strings.TrimRight(result, ", ")
}

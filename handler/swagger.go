package handler

import (
	"bytes"
	"fmt"
	"html/template"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"github.com/xakepp35/anygate/config"
	"github.com/xakepp35/anygate/router"
)

type swaggerEntry struct {
	Name     string
	URL      string
	Priority int
}

type swaggerUIData struct {
	Entries     []swaggerEntry
	SingleURL   string
	PrimaryName string
	UIConfig    template.JS
}

const swaggerHTMLTemplate = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <title>Swagger UI</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.11.0/swagger-ui.css">
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5.11.0/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5.11.0/swagger-ui-standalone-preset.js"></script>
    <script>
      window.ui = SwaggerUIBundle({
        {{.UIConfig}}
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
</html>`

var swaggerUITemplate = template.Must(template.New("swaggerUI").Parse(swaggerHTMLTemplate))

func RegisterSwaggerMultiUI(r *router.Router, cfg config.Root) {
	log.Info().Msg("Registering Swagger UI handlers")

	readTimeout := 6 * time.Second
	writeTimeout := 6 * time.Second
	maxConnsPerHost := 100
	maxIdleConnDuration := 30 * time.Second

	if cfg.Proxy.ReadTimeout > 0 {
		readTimeout = cfg.Proxy.ReadTimeout
	}
	if cfg.Proxy.WriteTimeout > 0 {
		writeTimeout = cfg.Proxy.WriteTimeout
	}
	if cfg.Proxy.MaxConnsPerHost > 0 {
		maxConnsPerHost = cfg.Proxy.MaxConnsPerHost
	}
	if cfg.Proxy.MaxIdleConnDuration > 0 {
		maxIdleConnDuration = cfg.Proxy.MaxIdleConnDuration
	}

	client := &fasthttp.Client{
		ReadTimeout:         readTimeout,
		WriteTimeout:        writeTimeout,
		MaxConnsPerHost:     maxConnsPerHost,
		MaxIdleConnDuration: maxIdleConnDuration,
		Name:                cfg.Proxy.Name,
	}

	entryList := make([]swaggerEntry, 0, len(cfg.Swagger))

	var entryListMu sync.Mutex
	var wg sync.WaitGroup

	wg.Add(len(cfg.Swagger))

	// приоритет вывода документации соотносится с порядком в конфиге
	for prior, service := range cfg.Swagger {

		go func(name, remote string, prior int) {
			defer wg.Done()

			slug := slugify(name)

			// сначала пробуем достать спеку по исходному url
			log.Info().Msg("Trying to get spec from original url")
			status, body, err := HTTPGet(client, remote)
			if err == nil && status < 300 {
				// если вернулся ответ по исходному url, проверяем тело на наличие меток документации swagger (openapi)
				if isValidSwaggerSpec(body) {
					specPath := fmt.Sprintf("/swagger/specs/%s", slug)
					proxyH, proxyType := New(specPath, remote, cfg)
					if proxyType != "proxy" {
						return
					}

					RegisterRoute(r, "GET", specPath, wrapSpecProxy(withAcceptForSpecs(proxyH)))

					entryListMu.Lock()
					entryList = append(entryList, swaggerEntry{
						Name:     name,
						URL:      specPath,
						Priority: prior,
					})
					entryListMu.Unlock()

					return
				}
			}

			// если исходный url не выдал спеку - нормализуем и проверяем фолбеки (возможные ручки спеки)
			log.Warn().Err(err).Int("status", status).Msg("Failed to get spec from original url, applying fallbacks")

			base := normalizeBaseURL(remote)

			targetID, targetURL := findActiveFallbackRoute(client, base)
			if targetID < 0 {
				log.Error().Str("url", base).Str("reason", "no swagger urls found").Msg("No spec")
				return
			}

			specPath := fmt.Sprintf("/swagger/specs/%s-fallback-%d%s", slug, targetID, pickExt(targetURL))
			proxyH, proxyType := New(specPath, targetURL, cfg)
			if proxyType != "proxy" {
				log.Error().Str("url", base).Str("reason", "not a proxy type").Msg("No spec found")
				return
			}

			RegisterRoute(r, "GET", specPath, wrapSpecProxy(withAcceptForSpecs(proxyH)))

			entryListMu.Lock()
			entryList = append(entryList, swaggerEntry{
				Name:     name + " • fallback",
				URL:      specPath,
				Priority: prior,
			})
			entryListMu.Unlock()
		}(service.Name, service.URL, prior)

	}

	wg.Wait()

	// далее: сборка мультиинтерфейса Swagger

	// если одна спека — отдадим через `url:` (UI сразу поднимет её)
	var singleURL, primaryName string
	if len(entryList) == 1 {
		singleURL = entryList[0].URL
	} else if len(entryList) > 1 {
		// сортируем по приоритетам
		sort.Slice(entryList, func(i, j int) bool {
			return entryList[i].Priority < entryList[j].Priority
		})
		// возьмём имя первой спеки для "urls.primaryName"
		primaryName = entryList[0].Name
	}

	var uiConfig template.JS
	if singleURL != "" {
		uiConfig = template.JS(fmt.Sprintf(`url: %q,`, singleURL))
	} else {
		// ВАЖНО: правильный ключ с точкой — "urls.primaryName"
		uiConfig = template.JS(fmt.Sprintf(`urls: [%s],
        "urls.primaryName": %q,`,
			joinSwaggerEntries(entryList),
			primaryName,
		))
	}
	log.Info().Str("uiConfig", string(uiConfig)).Msg("Applying ui config")

	// Подготавливаем данные для шаблона
	templateData := swaggerUIData{
		Entries:     entryList,
		SingleURL:   singleURL,
		PrimaryName: primaryName,
		UIConfig:    uiConfig,
	}

	// Предварительно рендерим HTML шаблон
	var htmlBuf bytes.Buffer
	if err := swaggerUITemplate.Execute(&htmlBuf, templateData); err != nil {
		log.Error().Err(err).Msg("Failed to render Swagger UI template")
		return
	}
	renderedHTML := htmlBuf.String()

	handler := func(ctx *fasthttp.RequestCtx) {
		log.Info().Str("path", string(ctx.Path())).Int("specs", len(entryList)).Msg("Serving Swagger UI")
		ctx.SetContentType("text/html; charset=utf-8")
		ctx.SetStatusCode(fasthttp.StatusOK)
		_, _ = ctx.WriteString(renderedHTML)
	}
	r.Register("GET", "/swagger", handler)
	r.Register("GET", "/swagger/index.html", handler)
	log.Info().Int("specs_in_ui", len(entryList)).Msg("Swagger UI handlers registered")
}

// isValidSwaggerSpec проверяет, что тело ответа содержит валидную OpenAPI/Swagger спецификацию
func isValidSwaggerSpec(body []byte) bool {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return false
	}

	// JSON формат
	if body[0] == '{' {
		return bytes.Contains(body, []byte(`"swagger"`)) ||
			bytes.Contains(body, []byte(`"openapi"`))
	}

	// YAML формат
	return bytes.HasPrefix(body, []byte("swagger:")) ||
		bytes.HasPrefix(body, []byte("openapi:")) ||
		(bytes.HasPrefix(body, []byte("---")) &&
			(bytes.Contains(body, []byte("\nswagger:")) ||
				bytes.Contains(body, []byte("\nopenapi:"))))
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
		if err == nil && (status >= 200 && status < 300) {
			log.Info().Str("url", c).Msg("Found active fallback swagger route")
			return i, c
		}
	}

	return -1, ""
}

func joinSwaggerEntries(entries []swaggerEntry) string {
	if len(entries) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(entries) * 64)

	for i, e := range entries {
		if i > 0 {
			builder.WriteString(", ")
		}
		_, err := fmt.Fprintf(&builder, "{name: %q, url: %q}", e.Name, e.URL)
		if err != nil {
			continue
		}
	}

	return builder.String()
}

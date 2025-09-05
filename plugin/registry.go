package plugin

import "github.com/rs/zerolog/log"

type Registry = map[string]Constructor

var registry = Registry{
	"rate_limiter": RateLimiter,
	"cors":         CorsConstructor,
}

// какие опции тут у нас есть?
// timeout - не плагин, сделан на уровне прокси хендлера
// X-Request-ID должен генерироваться фронтом по формату 2025-09-05-12-01-01-123-df98y7thbdftoh7bbohdi и хендлер чтобы гарантировал его возврат на фронт (если он есть)
// cors	allowOrigins, allowMethods	CORS-заголовки
// logger	level, fields, target	написать Логгирование, продумать модель скрытия sensitive data
// headers	request: [], response: [] - наборы инструкуий вида add, remove, set	 Управление HTTP-заголовками

// 🧙 BuildChain — алхимик исполнения: каждое зелье добавляет эффект.
func BuildChain(handler Handler, specs ...Spec) Handler {
	for _, spec := range specs {
		constructor, ok := registry[spec.Kind]
		if !ok {
			log.Fatal().Any("spec", spec).Msg("registry")
		}
		fn, err := constructor(spec.Args)
		if err != nil {
			log.Fatal().Any("spec", spec).Err(err).Msg("constructor")
		}
		handler = fn(handler)
	}
	return handler
}

package plugin

import "github.com/valyala/fasthttp"

const (
	Origin         = "origin"
	Methods        = "methods"
	AllowedHeaders = "allowedHeaders"
	Credentials    = "credentials"
	MaxAge         = "maxAge"
)

func CorsConstructor(args SpecArgs) (Func, error) {
	return func(next Handler) Handler {
		return func(ctx *fasthttp.RequestCtx) {
			isOptions := string(ctx.Method()) == "OPTIONS"

			var (
				origin      = "*"
				methods     = "GET,HEAD,PUT,PATCH,POST,DELETE"
				headers     = "Content-Type,Origin,Accept"
				credentials = false
				maxAge      string
			)

			for opt, value := range args {
				switch opt {
				case Origin:
					if v, ok := value.(string); ok {
						origin = v
					}
				case Methods:
					if v, ok := value.(string); ok {
						methods = v
					}
				case AllowedHeaders:
					if v, ok := value.(string); ok {
						headers = v
					}
				case Credentials:
					if v, ok := value.(bool); ok {
						credentials = v
					}
				case MaxAge:
					if v, ok := value.(string); ok {
						maxAge = v
					}
				}
			}

			ctx.Response.Header.Set("Access-Control-Allow-Origin", origin)
			ctx.Response.Header.Set("Access-Control-Allow-Methods", methods)
			ctx.Response.Header.Set("Access-Control-Allow-Headers", headers)
			if credentials {
				ctx.Response.Header.Set("Access-Control-Allow-Credentials", "true")
			}
			if maxAge != "" {
				ctx.Response.Header.Set("Access-Control-Max-Age", maxAge)
			}

			if isOptions {
				ctx.SetStatusCode(fasthttp.StatusNoContent)
				ctx.Response.Header.Set("Content-Length", "0")
				ctx.SetBody(nil)
				return
			}

			next(ctx)
		}
	}, nil
}

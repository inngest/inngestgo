package experimental

import "github.com/inngest/inngestgo/internal/middleware"

type BaseMiddleware = middleware.BaseMiddleware
type Middleware = middleware.Middleware

var LoggerFromContext = middleware.LoggerFromContext

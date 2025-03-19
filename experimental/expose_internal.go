package experimental

import "github.com/inngest/inngestgo/internal/middleware"

type BaseMiddleware = middleware.BaseMiddleware
type Middleware = middleware.Middleware
type TransformableInput = middleware.TransformableInput

var LoggerFromContext = middleware.LoggerFromContext

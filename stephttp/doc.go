// Package stephttp runs HTTP handlers as Inngest functions.  A handler uses
// step.Run and the other step tools, and Inngest records each request as a run.
// The client gets the handler's response with no wait for Inngest.
//
// # Setup
//
// Create one provider for the process with Setup.  Call Wait at shutdown,
// because each finished run goes to Inngest in the background after the
// response.  A run that is still being sent when the process exits is lost.
//
//	steps := stephttp.Setup(stephttp.SetupOpts{})
//
// # Three ways to run a function
//
// Wrap a handler with Handle or HandleFunc.  Every request to it is a function:
//
//	mux.Handle("POST /users", steps.HandleFunc(stephttp.FnOpts{}, createUser))
//
// Call Start on the first line of a handler that the provider cannot wrap, eg.
// a route that generated code registers.  The handler must defer end:
//
//	w, r, end, err := steps.Start(w, r, stephttp.FnOpts{ID: "create-user"})
//	defer end()
//	if err != nil {
//		return
//	}
//
// Put Middleware in front of many handlers, eg. a whole router.  A handler behind
// it is a function only after it opts in with Configure.  Until then, its steps
// run with no run, and the middleware copies no body unless
// MiddlewareOpts.RecordRequestBodies is set:
//
//	api.Use(steps.Middleware(stephttp.MiddlewareOpts{}))
//
//	stephttp.Configure(r.Context(), func(o *stephttp.FnOpts) {
//		o.ID = "create-user"
//	})
//
// # Configuration
//
// FnOpts configures one function.  Handle, HandleFunc, and Start take it at the
// start.  Configure changes it during a request, and changes only the fields
// that its func sets.  After the run starts, a change to ID does not apply.
//
// # Runs
//
// A function gets a run when its first step runs, or at the start when
// FnOpts.TrackAllRequests is set.  A request without a run sends no x-run-id
// header and makes no call to Inngest.  Each new run gets one attempt, so a step
// error goes back to the handler, and a handler that catches it still finishes
// the run.  An async step, eg. step.Sleep, makes the run continue in Inngest,
// and the client gets the FnOpts.AsyncResponse.  A panic in the handler gets a
// 500, and so does an async step when Inngest cannot start its run.
//
// # Request and response bodies
//
// The request body is copied as the handler reads it, because the handler
// reads it before the run exists.  The copy is stored only when the request gets
// a run, and only when FnOpts.OmitRequestBody is not set.  For Handle,
// HandleFunc, and Start, OmitRequestBody at the start stops the copy, so a body
// with secrets is never in memory twice.  Configure cannot start a copy that the
// start config omits.
//
// Behind Middleware, the body passes through with no copy until Configure.  A
// handler that calls Configure before it reads the body stores the whole body.
// A handler that reads the body first stores none, eg. GraphQL, which parses the
// operation first.  MiddlewareOpts.RecordRequestBodies copies every body from
// the start, so those handlers store it too.  That costs a copy on every
// request, so put it on the routes that need it.
//
// The response is stored from the first write, or behind Middleware from the
// point of Configure.  FnOpts.OmitResponseBody stops it.  The client always gets
// the full response.  MaxRequestBodySize and MaxResponseBodySize limit the
// stored copies, and both are 1 MiB by default.
//
// # Function IDs
//
// FnOpts.ID names the function.  If it is empty, the ID is the http.ServeMux
// pattern that routed the request, eg. "POST /users/{id}".  Without a pattern,
// Inngest builds the ID from the method and the path, and each value in a path
// such as /users/123 creates a separate function.  A warning is logged once for
// each wrapper when that happens.
package stephttp

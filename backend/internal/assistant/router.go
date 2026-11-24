package assistant

import (
	"context"
	"fmt"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"gitlab.com/tozd/go/errors"
)

// Route is a registered intent as recognizers see it.
type Route struct {
	Name  string
	Spec  Spec
	Slots []string // slot names of the intent type (fields tagged `slot`), sorted
}

type route struct {
	Route
	newIntent func() Intent // zero value of the registered concrete type
	handler   Handler       // type-asserts to the concrete type
}

// Router maps intent names to handlers, like http.ServeMux maps patterns.
// Register handlers with Handle; not safe for registration concurrent with
// use.
type Router struct {
	routes     map[string]*route
	order      []string // registration order, for Intents
	middleware []Middleware
}

// NewRouter returns an empty router.
func NewRouter() *Router {
	return &Router{routes: make(map[string]*route)}
}

// Handle registers h for intent type I (cf. watermill's
// cqrs.NewCommandHandler[Command] and the MCP Go SDK's AddTool[In, Out]).
// The name is taken from I's IntentName. Like http.ServeMux, it panics on
// an empty or duplicate name: both are programming errors found at startup.
func Handle[I Intent](r *Router, spec Spec, h func(ctx context.Context, intent I) (Response, error)) {
	var zero I
	name := zero.IntentName()
	if name == "" {
		panic(fmt.Sprintf("assistant: intent type %T has an empty IntentName", zero))
	}
	if _, dup := r.routes[name]; dup {
		panic(fmt.Sprintf("assistant: intent %q registered twice", name))
	}
	slots := checkSlots(zero, spec)

	r.routes[name] = &route{
		Route:     Route{Name: name, Spec: spec, Slots: slots},
		newIntent: func() Intent { var v I; return v },
		handler: HandlerFunc(func(ctx context.Context, intent Intent) (Response, error) {
			v, ok := intent.(I)
			if !ok {
				return Response{}, errors.WithMessagef(fault.ErrUnexpectedType, "intent %q: got %T, want %T", name, intent, zero)
			}
			return h(ctx, v)
		}),
	}
	r.order = append(r.order, name)
}

// Use adds middleware around every handler. The first one added is the
// outermost.
func (r *Router) Use(mw ...Middleware) {
	r.middleware = append(r.middleware, mw...)
}

// Intents returns the registered intents in registration order.
func (r *Router) Intents() []Route {
	routes := make([]Route, len(r.order))
	for i, name := range r.order {
		routes[i] = r.routes[name].Route
	}
	return routes
}

// New returns the zero value of the intent type registered under name, for
// recognizers to fill in.
func (r *Router) New(name string) (Intent, error) {
	rt, ok := r.routes[name]
	if !ok {
		return nil, errors.WithMessagef(fault.ErrNotFound, "intent %q", name)
	}
	return rt.newIntent(), nil
}

// Dispatch runs the handler registered for the intent's name, through the
// middleware.
func (r *Router) Dispatch(ctx context.Context, intent Intent) (Response, error) {
	rt, ok := r.routes[intent.IntentName()]
	if !ok {
		return Response{}, errors.WithMessagef(fault.ErrNotFound, "intent %q", intent.IntentName())
	}
	h := rt.handler
	for i := len(r.middleware) - 1; i >= 0; i-- {
		h = r.middleware[i](h)
	}
	return h.Handle(ctx, intent)
}

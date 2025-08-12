package router

import (
	"strings"

	"github.com/valyala/fasthttp"
)

type node struct {
	prefix   string
	handlers map[string]fasthttp.RequestHandler
	wild     fasthttp.RequestHandler
	children []*node
}

type Router struct {
	root node
}

func New() *Router {
	return &Router{
		root: node{
			prefix: "/",
		},
	}
}

func (r *Router) Register(method, path string, handler fasthttp.RequestHandler) {
	switch method {
	case "ANY", "*":
		method = ""
	}
	insert(&r.root, path, method, handler)
}

func (r *Router) Handler(ctx *fasthttp.RequestCtx) {
	method := string(ctx.Method())
	path := string(ctx.Path())
	h := lookup(&r.root, path, method)
	if h == nil {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		return
	}
	h(ctx)
}

func insert(n *node, path string, method string, handler fasthttp.RequestHandler) {
	for {
		common := longestCommonPrefix(n.prefix, path)
		if common < len(n.prefix) {
			child := &node{
				prefix:   n.prefix[common:],
				handlers: n.handlers,
				wild:     n.wild, // ← переносим wild
				children: n.children,
			}
			n.prefix = path[:common]
			n.handlers = nil
			n.wild = nil
			n.children = []*node{child}
		}
		path = path[common:]
		if len(path) == 0 {
			if n.handlers == nil {
				n.handlers = make(map[string]fasthttp.RequestHandler, 1)
			}
			if method == "" {
				n.wild = handler
				return
			}
			n.handlers[method] = handler
			return
		}

		// ↓ корректный спуск к ребёнку
		found := false
		for _, child := range n.children {
			if strings.HasPrefix(path, child.prefix) {
				n = child
				found = true
				break
			}
		}
		if found {
			continue
		}

		newChild := &node{
			prefix:   path,
			handlers: map[string]fasthttp.RequestHandler{},
		}
		n.children = append(n.children, newChild)
		if method != "" {
			newChild.handlers[method] = handler
		} else {
			newChild.wild = handler
		}
		return
	}
}

func lookup(n *node, path string, method string) fasthttp.RequestHandler {
	for {
		if !strings.HasPrefix(path, n.prefix) {
			return nil
		}
		path = path[len(n.prefix):]

		if len(path) == 0 && n.handlers != nil {
			if h := n.handlers[method]; h != nil {
				return h
			}
			return n.wild
		}

		// ↓ корректный спуск к ребёнку
		found := false
		for _, child := range n.children {
			if strings.HasPrefix(path, child.prefix) {
				n = child
				found = true
				break
			}
		}
		if found {
			continue
		}

		if n.handlers != nil {
			if h := n.handlers[method]; h != nil {
				return h
			}
			return n.wild
		}
		return nil
	}
}

func longestCommonPrefix(a, b string) int {
	max := len(a)
	if len(b) < max {
		max = len(b)
	}
	i := 0
	for i < max && a[i] == b[i] {
		i++
	}
	return i
}

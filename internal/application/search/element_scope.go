package search

// ElementScope exposes the extractor's elementStack to other static HTML
// consumers. Call Start before inspecting the new element, then Push after
// inspecting it. Closed frame indexes are delivered from inner to outer scope.
// All implied-end and scope grammar remains in element_stack.go.
type ElementScope struct{ stack elementStack }

func NewElementScope(close func(int)) *ElementScope {
	return &ElementScope{stack: elementStack{close: func(frame openElement) { close(frame.index) }}}
}
func (s *ElementScope) Start(tag string) { s.stack.start(tag) }
func (s *ElementScope) End(tag string)   { s.stack.end(tag) }

// Push returns whether the element remains open. Void and self-closing elements
// follow the exact admission used by Extract and never become ancestors.
func (s *ElementScope) Push(tag string, index int, selfClosing bool) bool {
	if selfClosing || isVoid(tag) {
		return false
	}
	s.stack.open = append(s.stack.open, openElement{tag, index})
	return true
}

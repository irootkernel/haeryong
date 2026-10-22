package search

import "strings"

// elementStack retains only open-element frames and reports closed text ranges.
// These HTML scope/implied-end rules bound static descendants without building a
// DOM. In particular, list/table boundaries keep nested siblings in their own
// container, and ruby's rtc exception preserves its rt descendants.
type elementStack struct {
	open  []openElement
	close func(openElement)
}

const defaultStops = " applet caption html table td th marquee object template "
const tableStops = " html table template "
const impliedTags = " dd dt li optgroup option p rb rp rt rtc "

func tagIn(tag, names string) bool { return strings.Contains(names, " "+tag+" ") }
func (s *elementStack) popFrom(i int) {
	for j := len(s.open) - 1; j >= i; j-- {
		s.close(s.open[j])
	}
	s.open = s.open[:i]
}
func (s *elementStack) find(names, stops string) int {
	for i := len(s.open) - 1; i >= 0; i-- {
		tag := s.open[i].tag
		if tagIn(tag, names) {
			return i
		}
		if tagIn(tag, stops) {
			break
		}
	}
	return -1
}
func (s *elementStack) popScoped(names, stops string) {
	if i := s.find(names, stops); i >= 0 {
		s.popFrom(i)
	}
}
func (s *elementStack) implied(except string) {
	for len(s.open) > 0 {
		tag := s.open[len(s.open)-1].tag
		if tag == except || !tagIn(tag, impliedTags) {
			return
		}
		s.popFrom(len(s.open) - 1)
	}
}
func (s *elementStack) closeItem(names string) {
	for i := len(s.open) - 1; i >= 0; i-- {
		tag := s.open[i].tag
		if tagIn(tag, names) {
			s.popFrom(i)
			return
		}
		// List/definition-item start tags stop at a special element, except the
		// address/div/p elements through which HTML explicitly continues the scan.
		if !tagIn(tag, " address div p ") && tagIn(tag,
			" address applet area article aside base basefont bgsound blockquote body br button caption center col colgroup dd details dir div dl dt embed fieldset figcaption figure footer form frame frameset h1 h2 h3 h4 h5 h6 head header hgroup hr html iframe img input keygen li link listing main marquee menu meta nav noembed noframes noscript object ol p param plaintext pre script section select source style summary table tbody td template textarea tfoot th thead title tr track ul wbr xmp ") {
			return
		}
	}
}

func (s *elementStack) start(tag string) {
	if tagIn(tag, " address article aside blockquote center details dialog dir div dl fieldset figcaption figure footer form header hgroup main menu nav ol p pre listing search section summary table ul hr h1 h2 h3 h4 h5 h6 li dd dt ") {
		s.popScoped(" p ", defaultStops+" button ")
	}
	switch tag {
	case "li":
		s.closeItem(" li ")
	case "dt", "dd":
		s.closeItem(" dt dd ")
	case "option":
		if s.find(" select ", defaultStops) >= 0 {
			s.implied("optgroup")
		} else if len(s.open) > 0 && s.open[len(s.open)-1].tag == "option" {
			s.popFrom(len(s.open) - 1)
		}
	case "optgroup":
		if s.find(" select ", defaultStops) >= 0 {
			s.implied("")
		} else if len(s.open) > 0 && s.open[len(s.open)-1].tag == "option" {
			s.popFrom(len(s.open) - 1)
		}
	case "rb", "rtc":
		if s.find(" ruby ", defaultStops) >= 0 {
			s.implied("")
		}
	case "rt", "rp":
		if s.find(" ruby ", defaultStops) >= 0 {
			s.implied("rtc")
		}
	case "td", "th":
		s.popScoped(" td th ", " html table template tr ")
	case "tr":
		s.popScoped(" td th ", tableStops)
		s.popScoped(" tr ", tableStops)
	case "tbody", "thead", "tfoot", "caption", "colgroup":
		s.popScoped(" td th ", tableStops)
		s.popScoped(" tr ", tableStops)
		s.popScoped(" tbody thead tfoot caption colgroup ", tableStops)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		if len(s.open) > 0 && tagIn(s.open[len(s.open)-1].tag, " h1 h2 h3 h4 h5 h6 ") {
			s.popFrom(len(s.open) - 1)
		}
	}
}
func (s *elementStack) end(tag string) {
	switch tag {
	case "body", "html":
		// HTML after-body/after-after-body modes leave open elements in place.
		// Later content is processed in the current node; ordinary implied
		// start/end rules or EOF, not these tags, finalize descendant ranges.
		return
	case "p":
		s.popScoped(" p ", defaultStops+" button ")
	case "li":
		s.popScoped(" li ", defaultStops+" ol ul ")
	case "dt", "dd":
		s.popScoped(" "+tag+" ", defaultStops)
	case "td", "th", "tr", "tbody", "thead", "tfoot":
		s.popScoped(" "+tag+" ", tableStops)
	case "table":
		s.popScoped(" table ", tableStops)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		s.popScoped(" h1 h2 h3 h4 h5 h6 ", defaultStops)
	default:
		s.popScoped(" "+tag+" ", "")
	}
}

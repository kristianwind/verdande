// Package caldav implements the WebDAV and CalDAV verbs a task client needs
// (RFC 4791, over RFC 4918), so Apple Reminders and Thunderbird can read, create
// and complete tasks in verdande directly.
//
// Only the VTODO half is implemented, and only the parts a real client actually
// uses: PROPFIND to discover the collection, REPORT to fetch its contents, and
// GET/PUT/DELETE on individual items. Calendar scheduling, free/busy and the whole
// VEVENT side are not here, because nothing that reads tasks asks for them.
//
// XML is written as text rather than through encoding/xml. The namespace prefixing
// CalDAV requires — DAV:, caldav and calendarserver all interleaved in one
// document — is genuinely more awkward to express through struct tags than to write
// out, and the shape of these responses is fixed.
package caldav

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// Namespaces, with the prefixes clients expect to see.
const (
	NSDav    = "DAV:"
	NSCalDAV = "urn:ietf:params:xml:ns:caldav"
	NSCalSrv = "http://calendarserver.org/ns/"
	NSApple  = "http://apple.com/ns/ical/"
)

// PropfindRequest is the parsed body of a PROPFIND, which names the properties the
// client is asking about.
type PropfindRequest struct {
	// AllProp is set when the client asked for everything rather than a list.
	AllProp bool
	Props   []string
}

// ParsePropfind reads a PROPFIND body. An empty body means allprop, which is what
// the spec says and what some clients rely on.
func ParsePropfind(body []byte) PropfindRequest {
	if len(strings.TrimSpace(string(body))) == 0 {
		return PropfindRequest{AllProp: true}
	}

	var doc struct {
		XMLName xml.Name
		AllProp *struct{} `xml:"DAV: allprop"`
		Prop    struct {
			Any []xml.Name `xml:",any"`
		} `xml:"DAV: prop"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		// A body that will not parse is treated as allprop rather than as an
		// error: clients send surprising things, and answering with everything is
		// always a valid response to a PROPFIND.
		return PropfindRequest{AllProp: true}
	}
	if doc.AllProp != nil {
		return PropfindRequest{AllProp: true}
	}

	req := PropfindRequest{}
	for _, name := range doc.Prop.Any {
		req.Props = append(req.Props, name.Space+":"+name.Local)
	}
	return req
}

func (r PropfindRequest) Wants(space, local string) bool {
	if r.AllProp {
		return true
	}
	for _, p := range r.Props {
		if p == space+":"+local {
			return true
		}
	}
	return false
}

// Multistatus builds a 207 response body.
type Multistatus struct {
	responses []string
}

// AddCollection describes a calendar collection — the project itself.
func (m *Multistatus) AddCollection(href, displayName, ctag string, req PropfindRequest) {
	var props []string

	if req.Wants(NSDav, "resourcetype") {
		props = append(props, `<d:resourcetype><d:collection/><cal:calendar/></d:resourcetype>`)
	}
	if req.Wants(NSDav, "displayname") {
		props = append(props, `<d:displayname>`+escapeXML(displayName)+`</d:displayname>`)
	}
	if req.Wants(NSCalDAV, "supported-calendar-component-set") {
		// VTODO only. Announcing VEVENT as well would make Apple Calendar offer to
		// put appointments here, which it would then have nowhere to store.
		props = append(props,
			`<cal:supported-calendar-component-set><cal:comp name="VTODO"/></cal:supported-calendar-component-set>`)
	}
	if req.Wants(NSCalSrv, "getctag") {
		// The collection tag: clients poll it and only re-sync when it changes.
		// Getting this wrong means either constant full syncs or stale data.
		props = append(props, `<cs:getctag>`+escapeXML(ctag)+`</cs:getctag>`)
	}
	if req.Wants(NSDav, "getetag") {
		props = append(props, `<d:getetag>"`+escapeXML(ctag)+`"</d:getetag>`)
	}
	if req.Wants(NSApple, "calendar-color") {
		props = append(props, `<ical:calendar-color>#5FBF8FFF</ical:calendar-color>`)
	}
	if req.Wants(NSCalDAV, "calendar-description") {
		props = append(props, `<cal:calendar-description>`+escapeXML(displayName)+`</cal:calendar-description>`)
	}

	m.add(href, props)
}

// AddItem describes one VTODO resource.
func (m *Multistatus) AddItem(href, etag, calendarData string, req PropfindRequest) {
	var props []string

	if req.Wants(NSDav, "getetag") {
		props = append(props, `<d:getetag>"`+escapeXML(etag)+`"</d:getetag>`)
	}
	if req.Wants(NSDav, "resourcetype") {
		props = append(props, `<d:resourcetype/>`)
	}
	if req.Wants(NSDav, "getcontenttype") {
		props = append(props, `<d:getcontenttype>text/calendar; component=vtodo</d:getcontenttype>`)
	}
	if calendarData != "" && req.Wants(NSCalDAV, "calendar-data") {
		props = append(props, `<cal:calendar-data>`+escapeXML(calendarData)+`</cal:calendar-data>`)
	}

	m.add(href, props)
}

// AddPrincipal describes the user, which is what a client asks for first when it is
// working out where its calendars live.
func (m *Multistatus) AddPrincipal(href, calendarHome, displayName string, req PropfindRequest) {
	var props []string

	if req.Wants(NSDav, "resourcetype") {
		props = append(props, `<d:resourcetype><d:principal/></d:resourcetype>`)
	}
	if req.Wants(NSDav, "displayname") {
		props = append(props, `<d:displayname>`+escapeXML(displayName)+`</d:displayname>`)
	}
	if req.Wants(NSDav, "current-user-principal") || req.Wants(NSDav, "principal-URL") {
		props = append(props,
			`<d:current-user-principal><d:href>`+escapeXML(href)+`</d:href></d:current-user-principal>`,
			`<d:principal-URL><d:href>`+escapeXML(href)+`</d:href></d:principal-URL>`)
	}
	if req.Wants(NSCalDAV, "calendar-home-set") {
		props = append(props,
			`<cal:calendar-home-set><d:href>`+escapeXML(calendarHome)+`</d:href></cal:calendar-home-set>`)
	}

	m.add(href, props)
}

func (m *Multistatus) add(href string, props []string) {
	if len(props) == 0 {
		// A response with no properties still has to exist, or the client sees the
		// resource as absent rather than as having nothing it asked for.
		props = append(props, `<d:resourcetype/>`)
	}
	m.responses = append(m.responses, fmt.Sprintf(
		`<d:response><d:href>%s</d:href><d:propstat><d:prop>%s</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`,
		escapeXML(href), strings.Join(props, "")))
}

// AddNotFound records a resource the client asked about that does not exist. Needed
// by sync: it is how a client learns a task was deleted somewhere else.
func (m *Multistatus) AddNotFound(href string) {
	m.responses = append(m.responses, fmt.Sprintf(
		`<d:response><d:href>%s</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response>`,
		escapeXML(href)))
}

func (m *Multistatus) XML() string {
	return `<?xml version="1.0" encoding="utf-8"?>` +
		`<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav" ` +
		`xmlns:cs="http://calendarserver.org/ns/" xmlns:ical="http://apple.com/ns/ical/">` +
		strings.Join(m.responses, "") +
		`</d:multistatus>`
}

// --- REPORT ---------------------------------------------------------------------------

// ReportRequest is the parsed body of a calendar-query or calendar-multiget.
type ReportRequest struct {
	// Multiget lists specific hrefs; a query asks for everything matching a filter.
	Multiget bool
	Hrefs    []string
	Props    PropfindRequest
}

func ParseReport(body []byte) ReportRequest {
	var doc struct {
		XMLName xml.Name
		Prop    struct {
			Any []xml.Name `xml:",any"`
		} `xml:"DAV: prop"`
		Hrefs []string `xml:"DAV: href"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return ReportRequest{Props: PropfindRequest{AllProp: true}}
	}

	req := ReportRequest{
		Multiget: strings.Contains(strings.ToLower(doc.XMLName.Local), "multiget"),
		Hrefs:    doc.Hrefs,
	}
	for _, name := range doc.Prop.Any {
		req.Props.Props = append(req.Props.Props, name.Space+":"+name.Local)
	}
	if len(req.Props.Props) == 0 {
		req.Props.AllProp = true
	}
	return req
}

// escapeXML is deliberately its own function rather than xml.EscapeText: task titles
// arrive from users, and every one of them passes through here on its way into a
// document a client will parse. A missed escape is a client that fails to sync with
// a parse error nobody can see the cause of.
func escapeXML(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return ""
	}
	return b.String()
}

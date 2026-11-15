package artifacts

import (
	"strconv"
	"strings"

	"github.com/packvium/packvium-go/internal/jsonvalue"
)

// Each export is a pure function of the artifact: no solver, carrier, renderer or clock, so an
// export is replayable from the artifact it came from and every engine emits the same bytes.
//
//   - JSON is the artifact's RFC 8785 canonical form.
//   - CSV has one row per packing step and one per unplaced item. It is RFC 4180: a header
//     row, CRLF after every row, and a field quoted only when it holds a comma, quote, CR or LF.
//   - The work order is one self-contained HTML document: inline print styles, no script, no
//     external resource, ASCII source with entities for the few typographic characters.
//
// Values are copied, never re-rendered or reinterpreted. That includes a CSV field beginning
// with "=": prefixing it would change an identifier a warehouse system matches on, so opening
// the file in a spreadsheet is the reader's decision.

// CSVColumns is the CSV header, in column order.
var CSVColumns = []string{
	"record", "container_index", "container_type", "sequence", "item_type", "orientation",
	"x_ticks", "y_ticks", "z_ticks", "x", "y", "z", "length", "width", "height", "length_unit",
	"reason", "proof_level",
}

const style = "body{font-family:system-ui,sans-serif;margin:24px;color:#111}" +
	"h1{font-size:20px}h2{font-size:16px;margin-top:24px}" +
	"table{border-collapse:collapse;width:100%;margin:8px 0 16px}" +
	"th,td{border:1px solid #999;padding:4px 6px;text-align:left;font-size:12px;vertical-align:top}" +
	"th{background:#eee}" +
	".facts td:first-child{width:28%;font-weight:600}" +
	"@media print{body{margin:0}section.container{break-after:page}tr{break-inside:avoid}}"

const dash = "&mdash;"

var htmlEntities = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")

// ExportJSON is the artifact's canonical form.
func ExportJSON(artifact interface{}) (string, error) {
	document, err := require(artifact)
	if err != nil {
		return "", err
	}
	return Canonical(document.fields)
}

// ExportCSV is one row per work-order line, in plan step order, then one per unplaced item.
func ExportCSV(artifact interface{}) (string, error) {
	document, err := require(artifact)
	if err != nil {
		return "", err
	}
	rows := [][]interface{}{stringsToValues(CSVColumns)}
	workOrder := document.object("work_order")
	for _, container := range workOrder.list("containers") {
		for _, line := range container.list("lines") {
			rows = append(rows, stepRow(workOrder, container, line))
		}
	}
	for _, unplaced := range document.object("plan").list("unplaced") {
		facts := unplaced.object("facts")
		rows = append(rows, []interface{}{
			"unplaced", nil, nil, nil, facts.value("item_type"), nil, nil, nil, nil,
			nil, nil, nil, nil, nil, nil, nil, facts.value("reason"), facts.value("proof_level"),
		})
	}
	if document.err != nil {
		return "", document.err
	}
	var builder strings.Builder
	for _, row := range rows {
		for index, value := range row {
			if index > 0 {
				builder.WriteByte(',')
			}
			text, err := csvField(value)
			if err != nil {
				return "", err
			}
			builder.WriteString(text)
		}
		builder.WriteString("\r\n")
	}
	return builder.String(), nil
}

func stepRow(workOrder, container, line *reader) []interface{} {
	reference := line.object("placement")
	ticks := reference.object("position_ticks")
	position, dimensions := line.object("position"), line.object("dimensions")
	return []interface{}{
		"step", reference.value("container_index"), container.value("container_type"), line.optional("sequence", nil),
		reference.value("item_type"), reference.value("orientation"), ticks.value("x"), ticks.value("y"), ticks.value("z"),
		position.value("x"), position.value("y"), position.value("z"),
		dimensions.value("length"), dimensions.value("width"), dimensions.value("height"), workOrder.value("length_unit"),
		nil, nil,
	}
}

func csvField(value interface{}) (string, error) {
	text, err := textOf(value)
	if err != nil || !strings.ContainsAny(text, ",\"\r\n") {
		return text, err
	}
	return `"` + strings.Replace(text, `"`, `""`, -1) + `"`, nil
}

// ExportWorkOrderHTML is the printable work order: a facts table, one section per container
// with a checkbox per step, and a "Not packed" section.
func ExportWorkOrderHTML(artifact interface{}) (string, error) {
	document, err := require(artifact)
	if err != nil {
		return "", err
	}
	page := &page{}
	page.add("<!DOCTYPE html>", `<html lang="en">`, "<head>", `<meta charset="utf-8">`,
		"<title>Packing work order</title>", "<style>"+style+"</style>", "</head>", "<body>",
		"<h1>Packing work order</h1>", `<table class="facts">`)
	plan, workOrder, provenance := document.object("plan"), document.object("work_order"), document.object("provenance")
	page.factRows(document, plan, provenance)
	page.add("</table>")
	planContainers, workOrderContainers := plan.list("containers"), workOrder.list("containers")
	// As the reference's zip: one section per pair, as many as the shorter list holds.
	for index := 0; index < len(planContainers) && index < len(workOrderContainers); index++ {
		page.containerSection(planContainers[index], workOrderContainers[index], workOrder)
	}
	page.unplacedSection(plan.list("unplaced"))
	page.add("</body>", "</html>")
	if err := firstError(document.err, page.err); err != nil {
		return "", err
	}
	return strings.Join(page.lines, "\n") + "\n", nil
}

// page collects the work order's lines and the first value it could not render.
type page struct {
	lines []string
	err   error
}

func (p *page) add(lines ...string) { p.lines = append(p.lines, lines...) }

// escape is a value as HTML text; the first refusal is kept and the page is discarded.
func (p *page) escape(value interface{}) string {
	text, err := textOf(value)
	if err != nil && p.err == nil {
		p.err = err
	}
	return htmlEntities.Replace(text)
}

func (p *page) orDash(value interface{}) string {
	if value == nil {
		return dash
	}
	return p.escape(value)
}

func (p *page) factRows(document, plan, provenance *reader) {
	facts, replay, solver := plan.object("facts"), provenance.object("replay"), provenance.value("solver")
	feasibility := facts.value("feasibility")
	replayText := p.escape(replay.value("level"))
	if because := replay.value("because"); truthy(because) {
		replayText += " (" + p.escape(because) + ")"
	}
	solverText := dash
	if truthy(solver) {
		solverFields := provenance.object("solver")
		solverText = p.escape(solverFields.value("profile")) + " / " + p.escape(solverFields.value("solver")) +
			" / seed " + p.escape(solverFields.value("seed"))
	}
	var catalogs []string
	for _, catalog := range provenance.list("catalog_versions_used") {
		catalogs = append(catalogs, p.escape(catalog.optional("catalog_id", nil))+" v"+p.escape(catalog.optional("version", nil)))
	}
	catalogText := strings.Join(catalogs, ", ")
	if catalogText == "" {
		catalogText = dash
	}
	status := p.escape(facts.value("status"))
	objective := p.orDash(plan.value("objective"))
	count := p.escape(facts.value("container_count"))
	var terms []string
	for _, term := range facts.values("score") {
		terms = append(terms, p.escape(term))
	}
	var code interface{}
	if fields, isObject := feasibility.(map[string]interface{}); isObject {
		code = fields["code"]
	}
	rows := [][2]string{
		{"Status", status},
		{"Objective", objective},
		{"Containers", count},
		{"Score", "[" + strings.Join(terms, ", ") + "]"},
		{"Feasibility", p.orDash(code)},
		{"Replay", replayText},
		{"Solver", solverText},
		{"Catalogs", catalogText},
		{"Packvium", p.escape(document.value("suite_version"))},
	}
	for _, row := range rows {
		p.add("<tr><td>" + row[0] + "</td><td>" + row[1] + "</td></tr>")
	}
}

func (p *page) containerSection(planContainer, container, workOrder *reader) {
	facts := planContainer.object("facts")
	weightUnit, lengthUnit := p.escape(workOrder.value("weight_unit")), p.escape(workOrder.value("length_unit"))
	number, err := ordinal(container.value("container_index"))
	if err != nil && p.err == nil {
		p.err = err
	}
	heading := "<h2>Container " + number + ": " + p.orDash(container.value("container_type")) + "</h2>"
	weights := "<p>Payload " + p.escape(container.value("payload_weight")) + " " + weightUnit + " &middot; " +
		"Gross " + p.escape(container.value("gross_weight")) + " " + weightUnit + " &middot; " +
		"Utilization " + p.orDash(facts.value("volume_utilization")) + "</p>"
	p.add(`<section class="container">`, heading, weights)
	if planContainer.value("order") == "loading" {
		p.add("<p>Order: loading. Follow the steps in sequence.</p>")
	} else {
		p.add("<p>Order: unavailable. No safe loading order was supplied, so the steps are not numbered.</p>")
	}
	p.add("<table>",
		"<thead><tr><th>Step</th><th>Item</th><th>Orientation</th><th>Position x, y, z ("+lengthUnit+")</th>"+
			"<th>Size l &times; w &times; h ("+lengthUnit+")</th><th>Done</th></tr></thead>",
		"<tbody>")
	for _, line := range container.list("lines") {
		reference, position, size := line.object("placement"), line.object("position"), line.object("dimensions")
		p.add("<tr><td>" + p.escape(line.optional("sequence", "")) + "</td><td>" + p.escape(reference.value("item_type")) + "</td>" +
			"<td>" + p.escape(reference.value("orientation")) + "</td>" +
			"<td>" + p.escape(position.value("x")) + ", " + p.escape(position.value("y")) + ", " + p.escape(position.value("z")) + "</td>" +
			"<td>" + p.escape(size.value("length")) + " &times; " + p.escape(size.value("width")) + " &times; " + p.escape(size.value("height")) + "</td>" +
			"<td>&#9744;</td></tr>")
	}
	p.add("</tbody>", "</table>", "</section>")
}

func (p *page) unplacedSection(entries []*reader) {
	p.add("<section>", "<h2>Not packed</h2>")
	if len(entries) == 0 {
		p.add("<p>Every item was packed.</p>", "</section>")
		return
	}
	p.add("<table>", "<thead><tr><th>Item</th><th>Reason</th><th>Proof</th></tr></thead>", "<tbody>")
	for _, entry := range entries {
		facts := entry.object("facts")
		p.add("<tr><td>" + p.orDash(facts.value("item_type")) + "</td><td>" + p.orDash(facts.value("reason")) +
			"</td><td>" + p.orDash(facts.value("proof_level")) + "</td></tr>")
	}
	p.add("</tbody>", "</table>", "</section>")
}

// textOf is the one rendering every engine shares: a string as itself, an integer in decimal,
// null as nothing. Anything else -- a boolean, a float, an object -- has a different default
// spelling in each language, so it is refused rather than printed several ways.
func textOf(value interface{}) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	}
	return "", refuse(CodeInvalidValue, "a %s has no single rendering in a work order", typeName(value))
}

// ordinal is a container's 1-based number on the sheet. Computed, so it is checked like
// textOf: 0.5 + 1 would print 1.5 in one engine and be refused in another.
func ordinal(index interface{}) (string, error) {
	integer, ok := index.(int64)
	if !ok {
		return "", refuse(CodeInvalidValue, "container index %s is not an integer", repr(index))
	}
	return strconv.FormatInt(integer+1, 10), nil
}

// require reads only a document it knows how to read, and says so when it cannot.
//
// It reads the artifact through its canonical form, so an export is a function of the bytes
// every engine agrees on and not of how Go happens to hold them in memory: a float64 1.0 is
// the 1 the other engines read back from those bytes, and an int is an int64.
func require(artifact interface{}) (*reader, error) {
	fields, isObject := artifact.(map[string]interface{})
	var found interface{}
	if isObject {
		found = fields["format"]
	}
	if found != Format {
		return nil, refuse(CodeUnknownFormat, "cannot export format %s; this exporter reads %s", repr(found), Format)
	}
	canonical, err := Canonical(fields)
	if err != nil {
		return nil, err
	}
	decoded, err := jsonvalue.DecodeBytes([]byte(canonical))
	if err != nil {
		return nil, refuse(CodeInvalidJSON, "%s", err.Error())
	}
	document := &reader{fields: decoded.(map[string]interface{})}
	document.root = document
	return document, nil
}

// reader walks a document the exporters know the shape of. A member the format requires and
// the document lacks is recorded as unknown_format on the root, once, and reads as empty, so
// the exporters read straight through and check the error at the end.
type reader struct {
	fields map[string]interface{}
	root   *reader
	err    error
}

func (r *reader) fail(message string) {
	if r.root.err == nil {
		r.root.err = refuse(CodeUnknownFormat, "%s", message)
	}
}

func (r *reader) value(name string) interface{} {
	value, present := r.fields[name]
	if !present {
		r.fail("the document has no " + name + " where the format requires one")
	}
	return value
}

// optional is the reference's mapping.get(name, fallback).
func (r *reader) optional(name string, fallback interface{}) interface{} {
	if value, present := r.fields[name]; present {
		return value
	}
	return fallback
}

func (r *reader) object(name string) *reader {
	return r.child(r.value(name))
}

func (r *reader) child(value interface{}) *reader {
	fields, ok := value.(map[string]interface{})
	if !ok {
		r.fail("the document has a value where the format requires an object")
	}
	return &reader{fields: fields, root: r.root}
}

func (r *reader) values(name string) []interface{} {
	value := r.value(name)
	list, ok := value.([]interface{})
	if !ok {
		r.fail("the document has an object where the format requires a list")
	}
	return list
}

func (r *reader) list(name string) []*reader {
	values := r.values(name)
	readers := make([]*reader, len(values))
	for index, value := range values {
		readers[index] = r.child(value)
	}
	return readers
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// truthy is the reference's truth test for a decoded value.
func truthy(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	case []interface{}:
		return len(typed) != 0
	case map[string]interface{}:
		return len(typed) != 0
	}
	return true
}

func stringsToValues(names []string) []interface{} {
	values := make([]interface{}, len(names))
	for index, name := range names {
		values[index] = name
	}
	return values
}

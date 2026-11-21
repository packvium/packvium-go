package revisions_test

import (
	"crypto/sha256"
	"encoding/hex"
	"go/parser"
	"go/token"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/artifacts"
	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/revisions"
	"github.com/packvium/packvium-go/serialization"
)

// After the reference's tests/test_revisions.py; the cross-engine cases live in the
// conformance suite.

func cubeRequest() map[string]interface{} {
	return decode(`{"items": [
	  {"id": "cube", "quantity": 4, "weight": "1000", "dimensions": {"length": "100", "width": "100", "height": "100"}},
	  {"id": "slab", "quantity": 1, "dimensions": {"length": "200", "width": "100", "height": "50"}}],
	 "containers": [
	  {"id": "box", "quantity": 3, "inner_dimensions": {"length": "200", "width": "100", "height": "200"}},
	  {"id": "crate", "inner_dimensions": {"length": "300", "width": "200", "height": "200"}}],
	 "configuration": {"solver_profile": "balanced", "minimum_support_ratio": 1.0}}`).(map[string]interface{})
}

func decode(text string) interface{} {
	value, err := jsonvalue.DecodeBytes([]byte(text))
	if err != nil {
		panic(err)
	}
	return value
}

func artifactFor(t *testing.T, request map[string]interface{}) map[string]interface{} {
	t.Helper()
	result, err := serialization.Pack(request)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := artifacts.Build(request, decode(mustCanonical(t, result)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func mustCanonical(t *testing.T, value interface{}) string {
	t.Helper()
	text, err := jsonvalue.Compact(value)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func locked(kind string, sequence int64) interface{} {
	return decode(`{"sequence": ` + strconv.FormatInt(sequence, 10) + `, "type": "` + kind + `", "placement": {
	  "item_type": "cube", "container_type": "box", "position": {"x": "0", "z": "0"}, "orientation": "LWH"}}`)
}

func chain(t *testing.T) ([]map[string]interface{}, []interface{}) {
	t.Helper()
	root, err := revisions.RootRevision(cubeRequest())
	if err != nil {
		t.Fatal(err)
	}
	firstArtifact := artifactFor(t, root["request"].(map[string]interface{}))
	first, err := revisions.DeriveRevision(root, firstArtifact, []interface{}{locked("placement_locked", 1),
		decode(`{"sequence": 2, "type": "item_missing", "item_type": "cube", "quantity": 1}`)})
	if err != nil {
		t.Fatal(err)
	}
	secondArtifact := artifactFor(t, first["request"].(map[string]interface{}))
	second, err := revisions.DeriveRevision(first, secondArtifact, []interface{}{locked("placement_verified", 3)})
	if err != nil {
		t.Fatal(err)
	}
	return []map[string]interface{}{root, first, second}, []interface{}{nil, firstArtifact, secondArtifact}
}

func TestTheRootRecordsNothingAgainstTheRequest(t *testing.T) {
	root, err := revisions.RootRevision(cubeRequest())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{"format": revisions.Format, "suite_version": artifacts.SuiteVersion,
		"revision": int64(0), "parent": nil, "approved": nil, "events": []interface{}{}, "request": cubeRequest()}
	if !reflect.DeepEqual(root, want) {
		t.Errorf("%v", root)
	}
}

func TestARevisionLinksItsParentAndApprovedArtifactByDigest(t *testing.T) {
	chained, approved := chain(t)
	root, first := chained[0], chained[1]
	parent, _ := revisions.DocumentDigest(root)
	artifact, _ := revisions.DocumentDigest(approved[1])
	if first["revision"] != int64(1) || first["parent"] != parent ||
		first["approved"].(map[string]interface{})["artifact"] != artifact {
		t.Errorf("%v", first)
	}
	replay := approved[1].(map[string]interface{})["provenance"].(map[string]interface{})["replay"]
	if !reflect.DeepEqual(first["approved"].(map[string]interface{})["replay"], replay) {
		t.Error("approved.replay is not the artifact's provenance.replay")
	}
}

func TestADigestIsSHA256OverTheCanonicalBytes(t *testing.T) {
	root, _ := revisions.RootRevision(cubeRequest())
	text, _ := revisions.CanonicalJSON(root)
	sum := sha256.Sum256([]byte(text))
	if digest, _ := revisions.DocumentDigest(root); digest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Error(digest)
	}
}

func TestTransportSpellingNeverChangesADigest(t *testing.T) {
	root, _ := revisions.RootRevision(cubeRequest())
	reparsed := decode(mustCanonical(t, root)).(map[string]interface{})
	reparsed["request"].(map[string]interface{})["configuration"].(map[string]interface{})["minimum_support_ratio"] = int64(1)
	left, _ := revisions.DocumentDigest(reparsed)
	right, _ := revisions.DocumentDigest(root)
	if left != right {
		t.Error("1 and 1.0 hash differently")
	}
}

func TestTheSameInputsDeriveTheSameBytesAndAnIntactChainAudits(t *testing.T) {
	first, approved := chain(t)
	second, _ := chain(t)
	documents := make([]interface{}, len(first))
	for index := range first {
		left, _ := revisions.CanonicalJSON(first[index])
		right, _ := revisions.CanonicalJSON(second[index])
		if left != right {
			t.Errorf("revision %d differs between two derivations", index)
		}
		documents[index] = first[index]
	}
	issues, err := revisions.VerifyRevisionChain(documents, approved)
	if err != nil || len(issues) > 0 {
		t.Errorf("%v %v", issues, err)
	}
}

func TestAnEditedRequestAndADroppedRevisionAreReported(t *testing.T) {
	chained, _ := chain(t)
	edited := decode(mustCanonical(t, chained[1])).(map[string]interface{})
	edited["request"].(map[string]interface{})["items"].([]interface{})[0].(map[string]interface{})["quantity"] = int64(9)
	issues, err := revisions.VerifyRevisionChain([]interface{}{chained[0], edited, chained[2]}, nil)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	if !codes["request_mismatch"] || !codes["parent_mismatch"] {
		t.Errorf("%v", issues)
	}
	dropped, _ := revisions.VerifyRevisionChain([]interface{}{chained[0], chained[2]}, nil)
	if len(dropped) == 0 {
		t.Error("a dropped revision passed the audit")
	}
}

func TestAMissingItemLowersItsQuantityAndALastOneRemovesTheType(t *testing.T) {
	lowered, err := revisions.ApplyEvents(cubeRequest(), []interface{}{
		decode(`{"sequence": 1, "type": "item_missing", "item_type": "cube", "quantity": 3}`)})
	if err != nil {
		t.Fatal(err)
	}
	if quantity := lowered["items"].([]interface{})[0].(map[string]interface{})["quantity"]; quantity != int64(1) {
		t.Errorf("quantity %v", quantity)
	}
	removed, err := revisions.ApplyEvents(cubeRequest(), []interface{}{
		decode(`{"sequence": 1, "type": "item_missing", "item_type": "slab", "quantity": 1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if items := removed["items"].([]interface{}); len(items) != 1 {
		t.Errorf("%d item types left", len(items))
	}
}

func TestALockBecomesAFixedPlacementOnceAndAContradictionIsRefused(t *testing.T) {
	request, err := revisions.ApplyEvents(cubeRequest(), []interface{}{locked("placement_locked", 1),
		locked("placement_verified", 2)})
	if err != nil {
		t.Fatal(err)
	}
	if fixed := request["fixed_placements"].([]interface{}); len(fixed) != 1 {
		t.Errorf("%d fixed placements", len(fixed))
	}
	_, err = revisions.ApplyEvents(request, []interface{}{
		decode(`{"sequence": 3, "type": "item_missing", "item_type": "cube", "quantity": 4}`)})
	refusal, ok := err.(*revisions.Error)
	if !ok || refusal.Code != "event_conflict" || refusal.Message != "event 3: 1 cube are fixed, 0 would remain" {
		t.Errorf("%#v", err)
	}
}

func TestNothingPassedInIsChanged(t *testing.T) {
	request := cubeRequest()
	if _, err := revisions.ApplyEvents(request, []interface{}{locked("placement_locked", 1),
		decode(`{"sequence": 2, "type": "item_missing", "item_type": "cube", "quantity": 1}`)}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request, cubeRequest()) {
		t.Error("ApplyEvents changed its input")
	}
}

func TestTheBuilderImportsNoSolverValidatorOrClock(t *testing.T) {
	const module = "github.com/packvium/packvium-go/"
	allowed := map[string]bool{module + "artifacts": true, module + "execution": true,
		module + "internal/canonicaljson": true, module + "internal/jsonvalue": true,
		module + "internal/pyrepr": true, module + "internal/pointshape": true}
	forbidden := map[string]bool{"time": true, "math/rand": true, "net/http": true}
	visited := map[string]bool{}
	pending := []string{module + "revisions"}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if visited[current] {
			continue
		}
		visited[current] = true
		for _, imported := range packageImports(t, filepath.Join("..", strings.TrimPrefix(current, module))) {
			inModule := strings.HasPrefix(imported, module)
			if forbidden[imported] || (inModule && !allowed[imported]) {
				t.Errorf("%s imports %s", current, imported)
			}
			if inModule && allowed[imported] {
				pending = append(pending, imported)
			}
		}
	}
}

func packageImports(t *testing.T, directory string) []string {
	t.Helper()
	files, err := ioutil.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var imports []string
	for _, file := range files {
		name := file.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range parsed.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			imports = append(imports, path)
		}
	}
	return imports
}

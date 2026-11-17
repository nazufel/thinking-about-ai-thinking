package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// extra is one weakness on the second measure's list. The list itself, with
// the criterion for each item, is scoring/EXTRAS.md.
type extra struct {
	ID    string
	Title string
}

// extras mirrors the IDs in scoring/EXTRAS.md, in the same order.
var extras = []extra{
	{"U1", "archive listing reaches outside its directory"},
	{"U2", "old decision cached after a policy change"},
	{"U3", "decision cache has no size limit"},
	{"U4", "image references have no length limit"},
	{"U5", "a tenant token may equal the admin token"},
	{"U6", "token checks reveal token length"},
	{"U7", "audit trail is one buffer shared by all tenants"},
	{"U8", "cached approval outlives its attestation"},
	{"U9", "archive listing has no size limit"},
	{"U10", "symbolic links in the log directory are followed"},
}

// ExtrasFile is results/extras.json: the hand grades for the second measure.
type ExtrasFile struct {
	// ListHash is the hash of scoring/EXTRAS.md the grades were made against.
	ListHash string `json:"list_hash"`
	// Method says how the grades were produced.
	Method string `json:"method"`
	// Runs maps a run ID to the items its diff was credited with. Each
	// credit carries the reason, so a reader can check it against the diff.
	Runs map[string]map[string]string `json:"runs"`
}

func extrasPath(p Paths) string { return filepath.Join(filepath.Dir(p.Runs), "extras.json") }
func blindDir(p Paths) string   { return filepath.Join(p.Raw, "blind") }

// loadExtras reads the hand grades, or returns nil when there are none yet.
func loadExtras(p Paths) (*ExtrasFile, error) {
	data, err := os.ReadFile(extrasPath(p))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f ExtrasFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("results/extras.json: %w", err)
	}
	return &f, nil
}

// extrasCount returns how many listed items a run was credited with, and
// whether the run was graded at all.
func (f *ExtrasFile) extrasCount(id string) (int, bool) {
	if f == nil {
		return 0, false
	}
	credited, ok := f.Runs[id]
	if !ok {
		return 0, false
	}
	n := 0
	for _, e := range extras {
		if _, ok := credited[e.ID]; ok {
			n++
		}
	}
	return n, true
}

// blind copies the diff of every bug-hunt run in a plan under a random
// name, so that whoever grades them cannot tell which effort level a diff
// came from. The key that maps names back to runs is written next to them
// and must not be opened until grading is finished.
func blind(p Paths, plan string) error {
	recs, err := loadRecords(p)
	if err != nil {
		return err
	}
	dir := blindDir(p)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	key := map[string]string{}
	var codes []string
	for _, r := range recs {
		if r.Plan != plan || r.Task != "bughunt" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.Raw, r.ID+".diff"))
		if err != nil {
			return fmt.Errorf("run %s has no saved diff: %w", r.ID, err)
		}
		code, err := freshCode(key)
		if err != nil {
			return err
		}
		key[code] = r.ID
		codes = append(codes, code)
		if err := os.WriteFile(filepath.Join(dir, code+".diff"), data, 0o644); err != nil {
			return err
		}
	}
	if len(codes) == 0 {
		return fmt.Errorf("no bug-hunt runs recorded for plan %q", plan)
	}
	raw, _ := json.MarshalIndent(key, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "key.json"), append(raw, '\n'), 0o600); err != nil {
		return err
	}

	// Print the codes in sorted order, which is unrelated to run order.
	sort.Strings(codes)
	fmt.Printf("%d diffs to grade, in %s\n", len(codes), dir)
	for _, code := range codes {
		fmt.Println("  " + code + ".diff")
	}
	fmt.Println("Grade each one against scoring/EXTRAS.md and save the grades as grades.json in the same directory:")
	fmt.Println(`  {"<code>": {"U3": "why the diff earns this credit, with file and line"}, "<code>": {}}`)
	fmt.Println("Do not open key.json until every diff is graded. Then run: go run ./harness extras-unblind")
	return nil
}

func freshCode(taken map[string]string) (string, error) {
	for {
		b := make([]byte, 3)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		code := hex.EncodeToString(b)
		if _, ok := taken[code]; !ok {
			return code, nil
		}
	}
}

// unblind joins the grades with the key and writes results/extras.json.
func unblind(p Paths, method string) error {
	dir := blindDir(p)
	var key map[string]string
	if err := readJSON(filepath.Join(dir, "key.json"), &key); err != nil {
		return err
	}
	var grades map[string]map[string]string
	if err := readJSON(filepath.Join(dir, "grades.json"), &grades); err != nil {
		return err
	}

	known := map[string]bool{}
	for _, e := range extras {
		known[e.ID] = true
	}
	out := ExtrasFile{Method: method, Runs: map[string]map[string]string{}}
	if existing, err := loadExtras(p); err != nil {
		return err
	} else if existing != nil {
		out.Runs = existing.Runs
	}
	for code, id := range key {
		credited, ok := grades[code]
		if !ok {
			return fmt.Errorf("diff %s has no grade yet: every diff must be graded before unblinding", code)
		}
		for item, reason := range credited {
			if !known[item] {
				return fmt.Errorf("diff %s is credited with %q, which is not on the list", code, item)
			}
			if reason == "" {
				return fmt.Errorf("diff %s is credited with %s without a reason", code, item)
			}
		}
		if credited == nil {
			credited = map[string]string{}
		}
		out.Runs[id] = credited
	}
	for code := range grades {
		if _, ok := key[code]; !ok {
			return fmt.Errorf("grades.json has an entry for %s, which is not one of the diffs", code)
		}
	}

	out.ListHash, _ = fileHash(filepath.Join(p.Root, "scoring", "EXTRAS.md"))
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(extrasPath(p), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote results/extras.json with grades for %d runs\n", len(out.Runs))
	return nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

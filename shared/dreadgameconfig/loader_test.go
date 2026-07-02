package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDataTableParsesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	err := os.WriteFile(path, []byte(`{
		"rows": {
			"Row1": {"m_name": "Alpha", "m_value": 42, "m_active": true},
			"Row2": {"m_name": "Beta", "m_value": 3.14, "m_active": false}
		},
		"row_count": 2
	}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	dt, err := LoadDataTable(path)
	if err != nil {
		t.Fatalf("LoadDataTable() error = %v", err)
	}
	if dt.RowCount != 2 {
		t.Fatalf("RowCount = %d, want 2", dt.RowCount)
	}
	if len(dt.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want 2", len(dt.Rows))
	}
}

func TestLoadDataTableReturnsErrorForMissingFile(t *testing.T) {
	_, err := LoadDataTable("/nonexistent/path.json")
	if err == nil {
		t.Fatal("LoadDataTable() expected error for missing file")
	}
}

func TestLoadDataTableReturnsErrorForInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	err := os.WriteFile(path, []byte(`{not valid json`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	_, err = LoadDataTable(path)
	if err == nil {
		t.Fatal("LoadDataTable() expected error for invalid JSON")
	}
}

func TestLoadDataTableHandlesEmptyRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	err := os.WriteFile(path, []byte(`{"rows": {}, "row_count": 0}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	dt, err := LoadDataTable(path)
	if err != nil {
		t.Fatalf("LoadDataTable() error = %v", err)
	}
	if len(dt.Rows) != 0 {
		t.Fatalf("len(Rows) = %d, want 0", len(dt.Rows))
	}
}

func TestLoadDataTableHandlesMissingRowsKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "norows.json")
	err := os.WriteFile(path, []byte(`{"row_count": 0}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	dt, err := LoadDataTable(path)
	if err != nil {
		t.Fatalf("LoadDataTable() error = %v", err)
	}
	if dt.Rows == nil {
		t.Fatal("Rows should be initialized, not nil")
	}
}

func TestRowGetStringReturnsStringValue(t *testing.T) {
	r := Row{"m_name": "Alpha"}
	if got := r.GetString("m_name"); got != "Alpha" {
		t.Fatalf("GetString() = %q, want %q", got, "Alpha")
	}
}

func TestRowGetStringReturnsEmptyForMissingKey(t *testing.T) {
	r := Row{}
	if got := r.GetString("missing"); got != "" {
		t.Fatalf("GetString() = %q, want empty", got)
	}
}

func TestRowGetStringConvertsNumericValues(t *testing.T) {
	r := Row{"m_int": float64(42), "m_float": 3.14}
	if got := r.GetString("m_int"); got != "42" {
		t.Fatalf("GetString(int) = %q, want %q", got, "42")
	}
	if got := r.GetString("m_float"); got != "3.14" {
		t.Fatalf("GetString(float) = %q, want %q", got, "3.14")
	}
}

func TestRowGetStringConvertsBoolValues(t *testing.T) {
	r := Row{"m_true": true, "m_false": false}
	if got := r.GetString("m_true"); got != "true" {
		t.Fatalf("GetString(true) = %q, want %q", got, "true")
	}
	if got := r.GetString("m_false"); got != "false" {
		t.Fatalf("GetString(false) = %q, want %q", got, "false")
	}
}

func TestRowGetStringHandlesNil(t *testing.T) {
	r := Row{"m_nil": nil}
	if got := r.GetString("m_nil"); got != "" {
		t.Fatalf("GetString(nil) = %q, want empty", got)
	}
}

func TestRowGetFloat64ReturnsFloatValue(t *testing.T) {
	r := Row{"m_val": 3.14}
	if got := r.GetFloat64("m_val"); got != 3.14 {
		t.Fatalf("GetFloat64() = %f, want 3.14", got)
	}
}

func TestRowGetFloat64ReturnsZeroForMissingKey(t *testing.T) {
	r := Row{}
	if got := r.GetFloat64("missing"); got != 0 {
		t.Fatalf("GetFloat64() = %f, want 0", got)
	}
}

func TestRowGetFloat64ParsesStringNumbers(t *testing.T) {
	r := Row{"m_str": "42.5"}
	if got := r.GetFloat64("m_str"); got != 42.5 {
		t.Fatalf("GetFloat64(string) = %f, want 42.5", got)
	}
}

func TestRowGetFloat64ConvertsBool(t *testing.T) {
	r := Row{"m_true": true, "m_false": false}
	if got := r.GetFloat64("m_true"); got != 1 {
		t.Fatalf("GetFloat64(true) = %f, want 1", got)
	}
	if got := r.GetFloat64("m_false"); got != 0 {
		t.Fatalf("GetFloat64(false) = %f, want 0", got)
	}
}

func TestRowGetIntConvertsFromFloat(t *testing.T) {
	r := Row{"m_val": float64(42)}
	if got := r.GetInt("m_val"); got != 42 {
		t.Fatalf("GetInt() = %d, want 42", got)
	}
}

func TestRowGetInt32ConvertsFromFloat(t *testing.T) {
	r := Row{"m_val": float64(100)}
	if got := r.GetInt32("m_val"); got != 100 {
		t.Fatalf("GetInt32() = %d, want 100", got)
	}
}

func TestRowGetInt64ConvertsFromFloat(t *testing.T) {
	r := Row{"m_val": float64(9999999)}
	if got := r.GetInt64("m_val"); got != 9999999 {
		t.Fatalf("GetInt64() = %d, want 9999999", got)
	}
}

func TestRowGetBoolReturnsBoolValue(t *testing.T) {
	r := Row{"m_true": true, "m_false": false}
	if got := r.GetBool("m_true"); !got {
		t.Fatal("GetBool(true) = false, want true")
	}
	if got := r.GetBool("m_false"); got {
		t.Fatal("GetBool(false) = true, want false")
	}
}

func TestRowGetBoolReturnsFalseForMissingKey(t *testing.T) {
	r := Row{}
	if got := r.GetBool("missing"); got {
		t.Fatal("GetBool() = true, want false")
	}
}

func TestRowGetBoolConvertsNumericValues(t *testing.T) {
	r := Row{"m_nonzero": float64(1), "m_zero": float64(0)}
	if got := r.GetBool("m_nonzero"); !got {
		t.Fatal("GetBool(1) = false, want true")
	}
	if got := r.GetBool("m_zero"); got {
		t.Fatal("GetBool(0) = true, want false")
	}
}

func TestRowGetBoolParsesStringBools(t *testing.T) {
	r := Row{"m_true": "true", "m_false": "false"}
	if got := r.GetBool("m_true"); !got {
		t.Fatal("GetBool(\"true\") = false, want true")
	}
	if got := r.GetBool("m_false"); got {
		t.Fatal("GetBool(\"false\") = true, want false")
	}
}

func TestRowHasReturnsTrueForExistingKey(t *testing.T) {
	r := Row{"m_key": "value"}
	if !r.Has("m_key") {
		t.Fatal("Has() = false, want true")
	}
}

func TestRowHasReturnsFalseForMissingKey(t *testing.T) {
	r := Row{}
	if r.Has("missing") {
		t.Fatal("Has() = true, want false")
	}
}

func TestRowGetMapReturnsNestedMap(t *testing.T) {
	r := Row{"m_nested": map[string]any{"inner": "value"}}
	m, ok := r.GetMap("m_nested")
	if !ok {
		t.Fatal("GetMap() ok = false, want true")
	}
	if m["inner"] != "value" {
		t.Fatalf("GetMap()[\"inner\"] = %v, want %q", m["inner"], "value")
	}
}

func TestRowGetMapReturnsFalseForNonMap(t *testing.T) {
	r := Row{"m_str": "not a map"}
	_, ok := r.GetMap("m_str")
	if ok {
		t.Fatal("GetMap() ok = true for string, want false")
	}
}

func TestRowGetSliceReturnsNestedSlice(t *testing.T) {
	r := Row{"m_list": []any{1.0, 2.0, 3.0}}
	s, ok := r.GetSlice("m_list")
	if !ok {
		t.Fatal("GetSlice() ok = false, want true")
	}
	if len(s) != 3 {
		t.Fatalf("len(GetSlice()) = %d, want 3", len(s))
	}
}

func TestRowGetSliceReturnsFalseForNonSlice(t *testing.T) {
	r := Row{"m_str": "not a slice"}
	_, ok := r.GetSlice("m_str")
	if ok {
		t.Fatal("GetSlice() ok = true for string, want false")
	}
}

func TestRowKeysReturnsAllFieldNames(t *testing.T) {
	r := Row{"a": 1, "b": 2, "c": 3}
	keys := r.Keys()
	if len(keys) != 3 {
		t.Fatalf("len(Keys()) = %d, want 3", len(keys))
	}
	seen := make(map[string]bool)
	for _, k := range keys {
		seen[k] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !seen[want] {
			t.Fatalf("Keys() missing %q", want)
		}
	}
}

func TestDataTableRowNamesReturnsAllRowNames(t *testing.T) {
	dt := &DataTable{
		Rows: map[string]Row{
			"Alpha": {"v": 1},
			"Beta":  {"v": 2},
			"Gamma": {"v": 3},
		},
		RowCount: 3,
	}
	names := dt.RowNames()
	if len(names) != 3 {
		t.Fatalf("len(RowNames()) = %d, want 3", len(names))
	}
	seen := make(map[string]bool)
	for _, n := range names {
		seen[n] = true
	}
	for _, want := range []string{"Alpha", "Beta", "Gamma"} {
		if !seen[want] {
			t.Fatalf("RowNames() missing %q", want)
		}
	}
}

func TestDataTableGetRowReturnsExistingRow(t *testing.T) {
	dt := &DataTable{
		Rows:     map[string]Row{"Alpha": {"m_val": float64(42)}},
		RowCount: 1,
	}
	r, ok := dt.GetRow("Alpha")
	if !ok {
		t.Fatal("GetRow() ok = false, want true")
	}
	if got := r.GetInt("m_val"); got != 42 {
		t.Fatalf("GetRow().GetInt() = %d, want 42", got)
	}
}

func TestDataTableGetRowReturnsFalseForMissing(t *testing.T) {
	dt := &DataTable{
		Rows:     map[string]Row{},
		RowCount: 0,
	}
	_, ok := dt.GetRow("Missing")
	if ok {
		t.Fatal("GetRow() ok = true for missing row, want false")
	}
}

func TestLoadDataTableParsesWeaponTableStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weapons.json")
	err := os.WriteFile(path, []byte(`{
		"rows": {
			"WP_AssaultMPri01_weapon01_BP": {
				"m_slotType": "YST_Primary",
				"m_class": "ASSAULT",
				"m_damageHigh": 160,
				"m_damageHighRange": 6000,
				"m_weaponCooldownTime": 0.7,
				"m_ignoreShields": false,
				"m_ammoMagazinSize": 25,
				"m_spreadBaseValue": 0.8
			}
		},
		"row_count": 1
	}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	dt, err := LoadDataTable(path)
	if err != nil {
		t.Fatalf("LoadDataTable() error = %v", err)
	}

	row, ok := dt.GetRow("WP_AssaultMPri01_weapon01_BP")
	if !ok {
		t.Fatal("missing weapon row")
	}
	if got := row.GetString("m_slotType"); got != "YST_Primary" {
		t.Fatalf("m_slotType = %q, want %q", got, "YST_Primary")
	}
	if got := row.GetString("m_class"); got != "ASSAULT" {
		t.Fatalf("m_class = %q, want %q", got, "ASSAULT")
	}
	if got := row.GetInt("m_damageHigh"); got != 160 {
		t.Fatalf("m_damageHigh = %d, want 160", got)
	}
	if got := row.GetInt("m_damageHighRange"); got != 6000 {
		t.Fatalf("m_damageHighRange = %d, want 6000", got)
	}
	if got := row.GetFloat64("m_weaponCooldownTime"); got != 0.7 {
		t.Fatalf("m_weaponCooldownTime = %f, want 0.7", got)
	}
	if got := row.GetBool("m_ignoreShields"); got {
		t.Fatal("m_ignoreShields = true, want false")
	}
	if got := row.GetInt("m_ammoMagazinSize"); got != 25 {
		t.Fatalf("m_ammoMagazinSize = %d, want 25", got)
	}
	if got := row.GetFloat64("m_spreadBaseValue"); got != 0.8 {
		t.Fatalf("m_spreadBaseValue = %f, want 0.8", got)
	}
}

func TestLoadDataTableParsesOfficerTableStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "officers.json")
	err := os.WriteFile(path, []byte(`{
		"rows": {
			"COMMUNICATIONS101": {
				"m_enabling": "OnAcquire()",
				"m_triggers": "OnEnable()",
				"m_effects": "AM(PawnAbilityCooldownTimeModifier -10%) : Stacks(1);",
				"m_stackOnAdding": false,
				"m_isPerkFeat": true
			}
		},
		"row_count": 1
	}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	dt, err := LoadDataTable(path)
	if err != nil {
		t.Fatalf("LoadDataTable() error = %v", err)
	}

	row, ok := dt.GetRow("COMMUNICATIONS101")
	if !ok {
		t.Fatal("missing officer row")
	}
	if got := row.GetString("m_enabling"); got != "OnAcquire()" {
		t.Fatalf("m_enabling = %q, want %q", got, "OnAcquire()")
	}
	if got := row.GetString("m_triggers"); got != "OnEnable()" {
		t.Fatalf("m_triggers = %q, want %q", got, "OnEnable()")
	}
	if got := row.GetString("m_effects"); got != "AM(PawnAbilityCooldownTimeModifier -10%) : Stacks(1);" {
		t.Fatalf("m_effects = %q", got)
	}
	if got := row.GetBool("m_stackOnAdding"); got {
		t.Fatal("m_stackOnAdding = true, want false")
	}
	if got := row.GetBool("m_isPerkFeat"); !got {
		t.Fatal("m_isPerkFeat = false, want true")
	}
}

func TestLoadDataTableParsesAssetReferenceStrings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bots.json")
	err := os.WriteFile(path, []byte(`{
		"rows": {
			"AssaultH_Default": {
				"m_clientBot": "[AssetObjectProperty:92b]"
			}
		},
		"row_count": 1
	}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	dt, err := LoadDataTable(path)
	if err != nil {
		t.Fatalf("LoadDataTable() error = %v", err)
	}

	row, ok := dt.GetRow("AssaultH_Default")
	if !ok {
		t.Fatal("missing bot row")
	}
	if got := row.GetString("m_clientBot"); got != "[AssetObjectProperty:92b]" {
		t.Fatalf("m_clientBot = %q, want %q", got, "[AssetObjectProperty:92b]")
	}
}

func TestLoadDataTableParsesTextPlaceholderStrings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ranks.json")
	err := os.WriteFile(path, []byte(`{
		"rows": {
			"0": {"m_rankName": "[text]"},
			"1": {"m_rankName": "[text]"}
		},
		"row_count": 2
	}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	dt, err := LoadDataTable(path)
	if err != nil {
		t.Fatalf("LoadDataTable() error = %v", err)
	}
	if len(dt.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want 2", len(dt.Rows))
	}
	row, ok := dt.GetRow("0")
	if !ok {
		t.Fatal("missing row 0")
	}
	if got := row.GetString("m_rankName"); got != "[text]" {
		t.Fatalf("m_rankName = %q, want %q", got, "[text]")
	}
}

func TestLoadDataTablePreservesNegativeNumbers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neg.json")
	err := os.WriteFile(path, []byte(`{
		"rows": {
			"Row1": {"m_val": -1.0, "m_int": -42}
		},
		"row_count": 1
	}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	dt, err := LoadDataTable(path)
	if err != nil {
		t.Fatalf("LoadDataTable() error = %v", err)
	}

	row, ok := dt.GetRow("Row1")
	if !ok {
		t.Fatal("missing row")
	}
	if got := row.GetFloat64("m_val"); got != -1.0 {
		t.Fatalf("m_val = %f, want -1.0", got)
	}
	if got := row.GetInt("m_int"); got != -42 {
		t.Fatalf("m_int = %d, want -42", got)
	}
}

func TestLoadDataTableHandlesLargeRowCounts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.json")

	rows := make(map[string]map[string]any)
	for i := 0; i < 226; i++ {
		key := "Weapon_" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		rows[key] = map[string]any{
			"m_damageHigh": float64(100 + i),
			"m_class":      "ASSAULT",
		}
	}

	data := fmt.Sprintf(`{"rows": %s, "row_count": 226}`, mustMarshalJSON(rows))
	err := os.WriteFile(path, []byte(data), 0644)
	if err != nil {
		t.Fatal(err)
	}

	dt, err := LoadDataTable(path)
	if err != nil {
		t.Fatalf("LoadDataTable() error = %v", err)
	}
	if len(dt.Rows) != 226 {
		t.Fatalf("len(Rows) = %d, want 226", len(dt.Rows))
	}
}

func mustMarshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

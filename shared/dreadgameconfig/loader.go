package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

type DataTable struct {
	Rows     map[string]Row `json:"rows"`
	RowCount int            `json:"row_count"`
}

type Row map[string]any

func LoadDataTable(path string) (*DataTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read datatable %s: %w", path, err)
	}
	var dt DataTable
	if err := json.Unmarshal(data, &dt); err != nil {
		return nil, fmt.Errorf("parse datatable %s: %w", path, err)
	}
	if dt.Rows == nil {
		dt.Rows = make(map[string]Row)
	}
	return &dt, nil
}

func (r Row) GetString(key string) string {
	v, ok := r[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(val)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}

func (r Row) GetFloat64(key string) float64 {
	v, ok := r[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	case bool:
		if val {
			return 1
		}
		return 0
	case nil:
		return 0
	default:
		return 0
	}
}

func (r Row) GetInt(key string) int {
	return int(r.GetFloat64(key))
}

func (r Row) GetInt32(key string) int32 {
	return int32(r.GetFloat64(key))
}

func (r Row) GetInt64(key string) int64 {
	return int64(r.GetFloat64(key))
}

func (r Row) GetBool(key string) bool {
	v, ok := r[key]
	if !ok {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case float64:
		return val != 0
	case string:
		b, _ := strconv.ParseBool(val)
		return b
	case nil:
		return false
	default:
		return false
	}
}

func (r Row) Has(key string) bool {
	_, ok := r[key]
	return ok
}

func (r Row) GetMap(key string) (map[string]any, bool) {
	v, ok := r[key]
	if !ok {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

func (r Row) GetSlice(key string) ([]any, bool) {
	v, ok := r[key]
	if !ok {
		return nil, false
	}
	s, ok := v.([]any)
	return s, ok
}

func (r Row) Keys() []string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	return keys
}

func (dt *DataTable) RowNames() []string {
	names := make([]string, 0, len(dt.Rows))
	for name := range dt.Rows {
		names = append(names, name)
	}
	return names
}

func (dt *DataTable) GetRow(name string) (Row, bool) {
	r, ok := dt.Rows[name]
	return r, ok
}

package critic

import "testing"

func TestReview(t *testing.T) {
	res := []byte(`{"rows":[{"ref":"INV-1","amount":120.5}],"total":120.5,"note":"ok"}`)
	cases := []struct {
		checks []Check
		ok     bool
	}{
		{[]Check{{Path: "rows", Op: "nonempty"}, {Path: "rows.0.ref", Op: "equals", Value: "INV-1"}}, true},
		{[]Check{{Path: "total", Op: "gte", Value: 100.0}, {Path: "total", Op: "lte", Value: 200}}, true},
		{[]Check{{Path: "note", Op: "contains", Value: "o"}}, true},
		{[]Check{{Path: "missing", Op: "exists"}}, false},
		{[]Check{{Path: "rows.3", Op: "exists"}}, false},
		{[]Check{{Path: "total", Op: "equals", Value: 99}}, false},
	}
	for i, c := range cases {
		v := Review(res, c.checks)
		if v.OK != c.ok {
			t.Errorf("case %d: got %+v", i, v)
		}
		if !v.OK && v.Failed == nil {
			t.Errorf("case %d: failure without the failed check", i)
		}
	}
}

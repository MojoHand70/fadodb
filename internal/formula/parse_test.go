package formula

import "testing"

func TestParseExact(t *testing.T) {
	cases := []struct {
		src  string
		typ  Type
		want string
		code string
	}{
		{"12.30", DecimalType(2), "12.30", ""},
		{"12.3", DecimalType(2), "12.30", ""},
		{"0", DecimalType(2), "0.00", ""},
		{"-7", DecimalType(0), "-7", ""},
		{"12.345", DecimalType(2), "", ErrInexact},
		{"abc", DecimalType(2), "", ErrBadText},
		{"12.30 PLN", MoneyType(2), "12.30 PLN", ""},
		{"12.3 EUR", MoneyType(2), "12.30 EUR", ""},
		{"12.30", MoneyType(2), "", ErrBadText},
		{"12.30 pln", MoneyType(2), "", ErrBadText},
		{"1", Scalar(KindNumber), "", ErrBadText},
	}
	for _, tc := range cases {
		v, err := ParseExact(tc.src, tc.typ)
		switch {
		case tc.code == "" && err != nil:
			t.Errorf("ParseExact(%q, %s): %v", tc.src, tc.typ, err)
		case tc.code == "" && Render(v) != tc.want:
			t.Errorf("ParseExact(%q, %s) = %s, want %s", tc.src, tc.typ, Render(v), tc.want)
		case tc.code != "" && (err == nil || err.Code != tc.code):
			t.Errorf("ParseExact(%q, %s): want %s, got %v", tc.src, tc.typ, tc.code, err)
		}
	}
}

func TestExactFromFloat(t *testing.T) {
	if v, ok := ExactFromFloat(0, DecimalType(2)); !ok || Render(v) != "0.00" {
		t.Errorf("0 -> %v %v", v, ok)
	}
	if v, ok := ExactFromFloat(12.3, DecimalType(2)); !ok || Render(v) != "12.30" {
		t.Errorf("12.3 -> %v %v", v, ok)
	}
	if _, ok := ExactFromFloat(0.125, DecimalType(2)); ok {
		t.Error("0.125 fits two places")
	}
	if _, ok := ExactFromFloat(1, MoneyType(2)); ok {
		t.Error("a float never makes a money: it has no currency")
	}
}

func TestRescale(t *testing.T) {
	v, err := Rescale(Decimal(123, 1), 3)
	if err != nil || Render(v) != "12.300" {
		t.Errorf("up: %v %v", v, err)
	}
	v, err = Rescale(Money(12300, 3, "PLN"), 2)
	if err != nil || Render(v) != "12.30 PLN" {
		t.Errorf("down, exact: %v %v", v, err)
	}
	if _, err = Rescale(Decimal(12345, 3), 2); err == nil || err.Code != ErrInexact {
		t.Errorf("down, inexact: %v", err)
	}
	if v, err = Rescale(Number(1.5), 2); err != nil || Render(v) != "1.5" {
		t.Errorf("a number is left alone: %v %v", v, err)
	}
	if v, err = Rescale(Blank(), 2); err != nil || !v.IsBlank() {
		t.Errorf("a blank stays blank: %v %v", v, err)
	}
}

func TestDay(t *testing.T) {
	for _, tc := range []struct {
		y, m, d int
		want    string
	}{{1970, 1, 1, "1970-01-01"}, {2026, 11, 8, "2026-11-08"}, {2000, 2, 29, "2000-02-29"}, {1969, 12, 31, "1969-12-31"}, {1, 1, 1, "0001-01-01"}, {9999, 12, 31, "9999-12-31"}} {
		if got := Render(Day(tc.y, tc.m, tc.d)); got != tc.want {
			t.Errorf("Day(%d, %d, %d) = %s, want %s", tc.y, tc.m, tc.d, got, tc.want)
		}
	}
}

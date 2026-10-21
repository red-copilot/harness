package naming

import "testing"

func TestIdentifierKeepsSafeCharactersAndReplacesTheRest(t *testing.T) {
	cases := []struct{ value, fallback, want string }{
		{value: "run-1_session", fallback: "x", want: "run-1_session"},
		{value: "run/1 session", fallback: "x", want: "run_1_session"},
		{value: "容器-1", fallback: "x", want: "__-1"},
		{value: "", fallback: "target", want: "target"},
	}
	for _, test := range cases {
		if got := Identifier(test.value, test.fallback); got != test.want {
			t.Fatalf("Identifier(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

package pluginversion

import "testing"

func TestExtract(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "export const",
			src:  `export const GROVE_PLUGIN_VERSION = "2.0.0";`,
			want: "2.0.0",
		},
		{
			name: "extra whitespace",
			src:  `export const GROVE_PLUGIN_VERSION   =   "1.2.3-rc1";`,
			want: "1.2.3-rc1",
		},
		{
			name: "embedded in larger file",
			src:  "// header\nimport x from \"y\";\nexport const GROVE_PLUGIN_VERSION = \"1.1.0\";\nconst other = \"z\";",
			want: "1.1.0",
		},
		{
			name: "unstamped",
			src:  `export const SOMETHING_ELSE = "1.0.0";`,
			want: "",
		},
		{
			name: "empty",
			src:  "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Extract([]byte(tt.src)); got != tt.want {
				t.Errorf("Extract() = %q, want %q", got, tt.want)
			}
		})
	}
}

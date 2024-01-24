package controller

import "testing"

func Test_generateSliceName(t *testing.T) {
	type args struct {
		clusterName string
		namespace   string
		name        string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test1",
			args: args{
				name:        "demo",
				namespace:   "test",
				clusterName: "cluster1",
			},
			want: "dasuidbasi",
		},

		{
			name: "test2",
			args: args{
				name:        "it-a-really-long-name",
				namespace:   "it-a-really-long-namespace",
				clusterName: "cluster1",
			},
			want: "dasuidbasi",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := generateSliceName(tt.args.clusterName, tt.args.namespace, tt.args.name); len(got) > 39 {
				t.Errorf("generateSliceName() = %v, want %v", got, tt.want)
			} else {
				t.Logf("Got name:%s", got)
			}
		})
	}
}

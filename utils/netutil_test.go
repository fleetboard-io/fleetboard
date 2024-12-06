package utils

import (
	"reflect"
	"testing"
)

func TestHosts(t *testing.T) {
	type args struct {
		cidr  string
		index int
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "num1",
			args: args{
				cidr:  "10.16.0.0/16",
				index: 2,
			},
			want:    "10.16.0.2",
			wantErr: false,
		},
		{
			name: "num2",
			args: args{
				cidr:  "10.16.3.0/22",
				index: 2,
			},
			want:    "10.16.0.2",
			wantErr: false,
		},
		{
			name: "num3",
			args: args{
				cidr:  "10.16.8.0/22",
				index: 2,
			},
			want:    "10.16.8.2",
			wantErr: false,
		},
		{
			name: "num4",
			args: args{
				cidr:  "10.16.8.0/24",
				index: 10,
			},
			want:    "10.16.8.10",
			wantErr: false,
		},
		{
			name: "num5",
			args: args{
				cidr:  "10.16.8.0/30",
				index: 2,
			},
			want:    "10.16.8.2",
			wantErr: false,
		},
		{
			name: "num5",
			args: args{
				cidr:  "10.16.8.0/30",
				index: 8,
			},
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetIndexIPFromCIDR(tt.args.cidr, tt.args.index)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetSecondIpFromCIDR() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetSecondIpFromCIDR() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindClusterAvailableCIDR(t *testing.T) {
	type args struct {
		networkCIDR   string
		existingPeers []string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "test /24~/21 CIDR",
			args: args{
				networkCIDR:   "10.0.0.0/24",
				existingPeers: []string{},
			},
			want:    "10.0.0.0/25",
			wantErr: false,
		},
		{
			name: "test /20~/15 CIDR",
			args: args{
				networkCIDR:   "10.0.0.0/20",
				existingPeers: []string{},
			},
			want:    "10.0.0.0/22",
			wantErr: false,
		},
		{
			name: "test /14~/9 CIDR",
			args: args{
				networkCIDR:   "10.0.0.0/14",
				existingPeers: []string{},
			},
			want:    "10.0.0.0/17",
			wantErr: false,
		},
		{
			name: "test >=/8 CIDR",
			args: args{
				networkCIDR:   "10.0.0.0/8",
				existingPeers: []string{},
			},
			want:    "10.0.0.0/12",
			wantErr: false,
		},
		{
			name: "test /30 CIDR",
			args: args{
				networkCIDR:   "10.0.0.0/30",
				existingPeers: []string{},
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "test /16 CIDR",
			args: args{
				networkCIDR:   "10.0.0.0/16",
				existingPeers: []string{"10.0.0.0/18"},
			},
			want:    "10.0.64.0/18",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FindTunnelAvailableCIDR(tt.args.networkCIDR, tt.args.existingPeers)
			if (err != nil) != tt.wantErr {
				t.Errorf("FindTunnelAvailableCIDR() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("FindTunnelAvailableCIDR() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindNodeAvailableCIDR(t *testing.T) {
	type args struct {
		tunnelCIDR    string // consistent test
		networkCIDR   string
		existingPeers []string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "test /24~/21 CIDR",
			args: args{
				tunnelCIDR:    "10.0.0.0/24",
				networkCIDR:   "10.0.0.0/25",
				existingPeers: []string{},
			},
			want:    "10.0.0.0/26",
			wantErr: false,
		},
		{
			name: "test /20~/9 CIDR",
			args: args{
				tunnelCIDR:    "10.0.0.0/20",
				networkCIDR:   "10.0.0.0/22",
				existingPeers: []string{},
			},
			want:    "10.0.0.0/24",
			wantErr: false,
		},
		{
			name: "test >=/8 CIDR",
			args: args{
				tunnelCIDR:    "10.0.0.0/8",
				networkCIDR:   "10.0.0.0/12",
				existingPeers: []string{},
			},
			want:    "10.0.0.0/22",
			wantErr: false,
		},
		{
			name: "test /30 CIDR",
			args: args{
				tunnelCIDR:    "10.0.0.0/30",
				networkCIDR:   "10.0.0.0/30",
				existingPeers: []string{},
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "test /16 CIDR",
			args: args{
				tunnelCIDR:    "10.0.0.0/16",
				networkCIDR:   "10.0.0.0/18",
				existingPeers: []string{"10.0.0.0/24"},
			},
			want:    "10.0.1.0/24",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// consistent checking ahead
			clusterCIDR, err := FindTunnelAvailableCIDR(tt.args.tunnelCIDR, []string{})
			if (err != nil) != tt.wantErr {
				t.Errorf("FindTunnelAvailableCIDR() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && clusterCIDR != tt.args.networkCIDR {
				t.Errorf("invalid test case")
			}

			got, err := FindClusterAvailableCIDR(tt.args.networkCIDR, tt.args.existingPeers)
			if (err != nil) != tt.wantErr {
				t.Errorf("FindClusterAvailableCIDR() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("FindClusterAvailableCIDR() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindAvailableCIDR(t *testing.T) {
	type args struct {
		networkCIDR   string
		existingPeers []string
		networkBits   int
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "name1",
			args: args{
				networkCIDR:   "20.112.0.0/12",
				existingPeers: []string{"20.112.0.0/16", "20.112.16.0/16", "20.112.32.0/16"},
				networkBits:   16,
			},
			want:    "20.113.0.0/16",
			wantErr: false,
		},
		{
			name: "name2",
			args: args{
				networkCIDR:   "20.112.0.0/12",
				existingPeers: []string{"20.113.0.0/16", "20.112.16.0/16", "20.115.32.0/16"},
				networkBits:   16,
			},
			want:    "20.114.0.0/16",
			wantErr: false,
		},
		{
			name: "name3",
			args: args{
				networkCIDR:   "20.112.0.0/12",
				existingPeers: []string{"20.112.0.0/16", "20.113.16.0/16", "20.114.32.0/16"},
				networkBits:   16,
			},
			want:    "20.115.0.0/16",
			wantErr: false,
		},
		{
			name: "name4",
			args: args{
				networkCIDR:   "20.112.0.0/12",
				existingPeers: []string{"20.112.0.0/16"},
				networkBits:   16,
			},
			want:    "20.113.0.0/16",
			wantErr: false,
		},
		{
			name: "name5",
			args: args{
				networkCIDR:   "20.112.0.0/16",
				existingPeers: []string{"20.112.0.0/24", "20.112.1.0/24", "20.112.2.0/24"},
				networkBits:   24,
			},
			want:    "20.112.3.0/24",
			wantErr: false,
		},
		{
			name: "name6",
			args: args{
				networkCIDR:   "20.112.0.0/16",
				existingPeers: []string{""},
				networkBits:   24,
			},
			want:    "20.112.0.0/24",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findAvailableCIDR(tt.args.networkCIDR, tt.args.existingPeers, tt.args.networkBits)
			if (err != nil) != tt.wantErr {
				t.Errorf("findAvailableCIDR() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("findAvailableCIDR() got = %v, want %v", got, tt.want)
			}
		})
	}
}

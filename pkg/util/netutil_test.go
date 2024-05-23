package util

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

func Test_findAvailableCIDR(t *testing.T) {
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
			name: "name1",
			args: args{
				networkCIDR:   "20.112.0.0/12",
				existingPeers: []string{"20.112.0.0/16", "20.112.16.0/16", "20.112.32.0/16"},
			},
			want:    "20.113.0.0/16",
			wantErr: false,
		},
		{
			name: "name1",
			args: args{
				networkCIDR:   "20.112.0.0/12",
				existingPeers: []string{"20.113.0.0/16", "20.112.16.0/16", "20.115.32.0/16"},
			},
			want:    "20.114.0.0/16",
			wantErr: false,
		},
		{
			name: "name1",
			args: args{
				networkCIDR:   "20.112.0.0/12",
				existingPeers: []string{"20.112.0.0/16", "20.113.16.0/16", "20.114.32.0/16"},
			},
			want:    "20.115.0.0/16",
			wantErr: false,
		},
		{
			name: "namex",
			args: args{
				networkCIDR:   "20.112.0.0/12",
				existingPeers: []string{"20.112.0.0/16"},
			},
			want:    "20.113.0.0/16",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FindAvailableCIDR(tt.args.networkCIDR, tt.args.existingPeers)
			if (err != nil) != tt.wantErr {
				t.Errorf("FindAvailableCIDR() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("FindAvailableCIDR() got = %v, want %v", got, tt.want)
			}
		})
	}
}

package clustermesh

import "testing"

func TestGetClusterMeshSecurityGroupName(t *testing.T) {
	type args struct {
		clusterName string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test1",
			args: args{
				clusterName: "test1",
			},
			want: "clustermesh-test1-sg",
		},
		{
			name: "test1",
			args: args{
				clusterName: "",
			},
			want: "clustermesh--sg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetClusterMeshSecurityGroupName(tt.args.clusterName); got != tt.want {
				t.Errorf("GetClusterMeshSecurityGroupName() = %v, want %v", got, tt.want)
			}
		})
	}
}

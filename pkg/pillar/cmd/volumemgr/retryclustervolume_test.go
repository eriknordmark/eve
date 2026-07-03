// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package volumemgr

import "testing"

func TestIsRetryableClusterStorageError(t *testing.T) {
	tests := []struct {
		name string
		err  string
		want bool
	}{
		{
			name: "observed cdi upload race",
			err:  "Error converting /persist/vault/volumes/c1c127b8-pvc-0.img to PVC c1c127b8-pvc-0: PVC Upload for pvc:c1c127b8-pvc-0 attempts to upload image failed, no upload pod annotation",
			want: true,
		},
		{
			name: "storageclass not ready",
			err:  "Error creating PVC foo: storageclass \"longhorn\" not found",
			want: true,
		},
		{
			name: "k8s api not reachable yet",
			err:  "failed to get clientset Get \"https://127.0.0.1:6443/api\": dial tcp 127.0.0.1:6443: connect: connection refused",
			want: true,
		},
		{
			name: "rollout wrapper",
			err:  "RolloutDiskToPVC: pvc:abc Failed after 312 seconds to convert qcow to PVC",
			want: true,
		},
		{
			name: "non-cluster error: image verification",
			err:  "doUpdateVol: content tree verification failed: bad signature",
			want: false,
		},
		{
			name: "non-cluster error: out of space",
			err:  "no space left on device while writing raw image",
			want: false,
		},
		{
			name: "empty",
			err:  "",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableClusterStorageError(tt.err); got != tt.want {
				t.Errorf("isRetryableClusterStorageError(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

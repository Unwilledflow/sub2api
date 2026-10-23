package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdaptivePoolUpdateValidate(t *testing.T) {
	tests := []struct {
		name    string
		input   AdaptivePoolUpdate
		wantErr string
	}{
		{
			name: "valid enabled pool",
			input: AdaptivePoolUpdate{
				ParentGroupID: 10,
				Enabled:       true,
				Members: []AdaptiveLeafRef{
					{LeafGroupID: 21, Enabled: true, SortOrder: 10},
					{LeafGroupID: 22, Enabled: false, SortOrder: 20},
				},
			},
		},
		{
			name: "enabled pool may omit members for auto-enroll",
			input: AdaptivePoolUpdate{
				ParentGroupID: 10,
				Enabled:       true,
			},
		},
		{
			name:    "parent must be positive",
			input:   AdaptivePoolUpdate{Enabled: false},
			wantErr: "parent group id must be positive",
		},
		{
			name: "enabled pool with disabled stored members is valid",
			input: AdaptivePoolUpdate{
				ParentGroupID: 10,
				Enabled:       true,
				Members:       []AdaptiveLeafRef{{LeafGroupID: 21}},
			},
		},
		{
			name: "self reference",
			input: AdaptivePoolUpdate{
				ParentGroupID: 10,
				Members:       []AdaptiveLeafRef{{LeafGroupID: 10}},
			},
			wantErr: "cannot reference itself",
		},
		{
			name: "duplicate leaf",
			input: AdaptivePoolUpdate{
				ParentGroupID: 10,
				Members: []AdaptiveLeafRef{
					{LeafGroupID: 21},
					{LeafGroupID: 21},
				},
			},
			wantErr: "duplicate adaptive leaf group id: 21",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

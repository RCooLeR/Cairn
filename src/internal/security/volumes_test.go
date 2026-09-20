package security

import (
	"reflect"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

func TestNormalizeCreateVolumeRequestLocalDriverPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		req        models.CreateVolumeRequest
		wantName   string
		wantDriver string
		wantOpts   map[string]string
		wantErr    bool
	}{
		{
			name:       "default local driver",
			req:        models.CreateVolumeRequest{Name: " data "},
			wantName:   "data",
			wantDriver: "local",
		},
		{
			name: "benign local nfs options normalize case whitespace and key order",
			req: models.CreateVolumeRequest{
				Name:   "cache",
				Driver: " LOCAL ",
				DriverOpts: map[string]string{
					" Device ": ":/exports/cache ",
					" O ":      " addr=10.0.0.2,username=ada,password=  keep spaces  , rw ",
					" TYPE ":   " nfs ",
				},
			},
			wantDriver: "local",
			wantOpts: map[string]string{
				"device": ":/exports/cache ",
				"o":      " addr=10.0.0.2,username=ada,password=  keep spaces  , rw ",
				"type":   " nfs ",
			},
		},
		{
			name: "default local bind root",
			req: models.CreateVolumeRequest{DriverOpts: map[string]string{
				"device": "/",
				"type":   "none",
				"o":      "bind",
			}},
			wantErr: true,
		},
		{
			name: "local bind is case and whitespace insensitive",
			req: models.CreateVolumeRequest{Driver: "LoCaL", DriverOpts: map[string]string{
				" O ":      " rw, BIND ",
				" DEVICE ": "/var/run/docker.sock",
				" TYPE ":   " NONE ",
			}},
			wantErr: true,
		},
		{
			name: "recursive bind sensitive path",
			req: models.CreateVolumeRequest{Driver: "local", DriverOpts: map[string]string{
				"type":   "none",
				"o":      "ro,rbind,nosuid",
				"device": "/etc",
			}},
			wantErr: true,
		},
		{
			name: "bind token with value is rejected conservatively",
			req: models.CreateVolumeRequest{Driver: "local", DriverOpts: map[string]string{
				"o":      "rw,bind=true",
				"device": `C:\Users`,
			}},
			wantErr: true,
		},
		{
			name: "ambiguous normalized option keys",
			req: models.CreateVolumeRequest{Driver: "local", DriverOpts: map[string]string{
				"o":   "rw",
				" O ": "ro",
			}},
			wantErr: true,
		},
		{
			name: "empty mount option token",
			req: models.CreateVolumeRequest{Driver: "local", DriverOpts: map[string]string{
				"o": "rw,,nosuid",
			}},
			wantErr: true,
		},
		{
			name: "control characters are malformed",
			req: models.CreateVolumeRequest{Driver: "local", DriverOpts: map[string]string{
				"device": "/srv/data\nother",
			}},
			wantErr: true,
		},
		{
			name: "custom driver options are outside local containment",
			req: models.CreateVolumeRequest{Driver: " Example/Plugin ", DriverOpts: map[string]string{
				"O":      "bind",
				"device": "/",
			}},
			wantDriver: "Example/Plugin",
			wantOpts: map[string]string{
				"O":      "bind",
				"device": "/",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeCreateVolumeRequest(tt.req)
			if tt.wantErr {
				if !apperror.IsCode(err, apperror.Conflict) {
					t.Fatalf("NormalizeCreateVolumeRequest() error = %v, want %s", err, apperror.Conflict)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeCreateVolumeRequest() error = %v", err)
			}
			if tt.wantName != "" && got.Name != tt.wantName {
				t.Fatalf("normalized name = %q, want %q", got.Name, tt.wantName)
			}
			if got.Driver != tt.wantDriver {
				t.Fatalf("driver = %q, want %q", got.Driver, tt.wantDriver)
			}
			if !reflect.DeepEqual(got.DriverOpts, tt.wantOpts) {
				t.Fatalf("driver opts = %#v, want %#v", got.DriverOpts, tt.wantOpts)
			}
			again, err := NormalizeCreateVolumeRequest(got)
			if err != nil {
				t.Fatalf("NormalizeCreateVolumeRequest(second call) error = %v", err)
			}
			if !reflect.DeepEqual(again, got) {
				t.Fatalf("second normalization = %#v, want idempotent %#v", again, got)
			}
		})
	}
}

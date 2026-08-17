package pool

import (
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/require"
)

func TestConfigSchema_OfflineGracePeriodAcceptsDocumentedDuration(t *testing.T) {
	schema := new(jsonschema.Reflector).Reflect(&Config{})
	definition := schema.Definitions["Config"]
	require.NotNil(t, definition)
	property, found := definition.Properties.Get("offlineGracePeriod")
	require.True(t, found)
	require.Equal(t, "string", property.Type)
}

package feature

import (
	"os"
	"testing"
)

func TestIsEnabled_DefaultFalse(t *testing.T) {
	Reset()

	os.Unsetenv(envName(FeatureNewSSE))

	if IsEnabled(FeatureNewSSE) {
		t.Fatal("ожидалось, что флаг FeatureNewSSE выключен по умолчанию")
	}
}

func TestIsEnabled_EnvVarTrue(t *testing.T) {
	Reset()

	os.Setenv(envName(FeatureNewUI), "true")
	defer os.Unsetenv(envName(FeatureNewUI))

	if !IsEnabled(FeatureNewUI) {
		t.Fatal("ожидалось, что флаг FeatureNewUI включён через FEATURE_NEW_UI=true")
	}
}

func TestIsEnabled_EnvVarFalse(t *testing.T) {
	Reset()

	os.Setenv(envName(FeatureExperimentalAPI), "false")
	defer os.Unsetenv(envName(FeatureExperimentalAPI))

	if IsEnabled(FeatureExperimentalAPI) {
		t.Fatal("ожидалось, что флаг FeatureExperimentalAPI выключен через FEATURE_EXPERIMENTAL_API=false")
	}
}

func TestIsEnabled_CacheAfterSet(t *testing.T) {
	Reset()

	SetEnabled(FeatureStrictConfig, true)
	if !IsEnabled(FeatureStrictConfig) {
		t.Fatal("ожидалось, что флаг FeatureStrictConfig включён после SetEnabled(true)")
	}
}

func TestIsEnabled_ResetClearsCache(t *testing.T) {
	Reset()

	SetEnabled(FeatureTelemetry, true)
	Reset()

	if IsEnabled(FeatureTelemetry) {
		t.Fatal("ожидалось, что флаг FeatureTelemetry выключен после Reset()")
	}
}

package kustomize

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/argoproj/pkg/exec"
	"github.com/stretchr/testify/assert"

	"github.com/argoproj/argo-cd/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/util/git"
)

const (
	kustomization1  = "kustomization_yaml"
	kustomization2a = "kustomization_yml"
	kustomization2b = "Kustomization"
	generators      = "generators"
)

func testDataDir(t *testing.T, src string) (string, func()) {
	res, err := ioutil.TempDir("", "kustomize-test")

	if err != nil {
		t.Fatalf("failed to create temp kustomize test directory. err: %s", err)
	}

	dest := filepath.Join(res, filepath.Base(src))

	_, err = exec.RunCommand("cp", exec.CmdOpts{}, "-r", src, dest)

	if err != nil {
		t.Fatalf("failed to copy src directory %s to kustomize test directory %s. err: %s", src, dest, err)
	}

	return dest, func() { _ = os.RemoveAll(dest) }
}

func testFileRead(t *testing.T, src string) string {
	file, err := os.Open(src)
	if err != nil {
		t.Fatalf("could not open file: %s", err)
	}

	defer func() {
		if err = file.Close(); err != nil {
			t.Fatalf("could close  opened file: %s", err)
		}
	}()

	b, err := ioutil.ReadAll(file)

	if err != nil {
		t.Fatalf("could read file: %s", err)
	}

	return string(b)
}

func TestKustomizeBuild(t *testing.T) {
	appPath, destroyDataDir := testDataDir(t, "./testdata/"+kustomization1)
	defer destroyDataDir()
	namePrefix := "namePrefix-"
	nameSuffix := "-nameSuffix"
	kustomize := NewKustomizeApp(appPath, git.NopCreds{}, "", "")
	kustomizeSource := v1alpha1.ApplicationSourceKustomize{
		NamePrefix: namePrefix,
		NameSuffix: nameSuffix,
		Images:     v1alpha1.KustomizeImages{"nginx:1.15.5"},
		CommonLabels: map[string]string{
			"app.kubernetes.io/managed-by": "argo-cd",
			"app.kubernetes.io/part-of":    "argo-cd-tests",
		},
	}
	objs, images, err := kustomize.Build(&kustomizeSource, nil)
	assert.Nil(t, err)
	if err != nil {
		assert.Equal(t, len(objs), 2)
		assert.Equal(t, len(images), 2)
	}
	for _, obj := range objs {
		switch obj.GetKind() {
		case "StatefulSet":
			assert.Equal(t, namePrefix+"web"+nameSuffix, obj.GetName())
			assert.Equal(t, map[string]string{
				"app.kubernetes.io/managed-by": "argo-cd",
				"app.kubernetes.io/part-of":    "argo-cd-tests",
			}, obj.GetLabels())
		case "Deployment":
			assert.Equal(t, namePrefix+"nginx-deployment"+nameSuffix, obj.GetName())
			assert.Equal(t, map[string]string{
				"app":                          "nginx",
				"app.kubernetes.io/managed-by": "argo-cd",
				"app.kubernetes.io/part-of":    "argo-cd-tests",
			}, obj.GetLabels())
		}
	}

	for _, image := range images {
		switch image {
		case "nginx":
			assert.Equal(t, "1.15.5", image)
		}
	}
}

func TestKustomizeConfigMapGeneratorLiterals(t *testing.T) {
	appPath, destroyDataDir := testDataDir(t, "./testdata/"+generators)

	defer destroyDataDir()
	kustomize := NewKustomizeApp(appPath, git.NopCreds{}, "", "")
	kustomizeSource := v1alpha1.ApplicationSourceKustomize{
		ConfigMapGenerators: v1alpha1.KustomizeConfigMapGenerators{
			v1alpha1.KustomizeConfigMapGenerator{
				Name:     "test-generator",
				Literals: []string{"keyA=value1", "keyB=value2"},
				Files:    []string{"./nginx.conf", "test-nginx.conf=./nginx.conf"},
			},
		},
	}

	objs, _, err := kustomize.Build(&kustomizeSource, nil)
	assert.Nil(t, err)
	if err != nil {
		assert.Equal(t, len(objs), 2)
	}

	var (
		configMap  v1.ConfigMap
		deployment appsv1.Deployment
	)

	for _, obj := range objs {
		switch obj.GetKind() {
		case "ConfigMap":
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), &configMap)
			assert.NoError(t, err)
		case "Deployment":
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), &deployment)
			assert.NoError(t, err)
		}
	}

	assert.Equal(t, "test-generator-fh7fbk75m8", configMap.Name)
	assert.Equal(t, configMap.Data["keyA"], "value1")
	assert.Equal(t, configMap.Data["keyB"], "value2")
	assert.Equal(t, configMap.Data["nginx.conf"], testFileRead(t, appPath+"/nginx.conf"))
	assert.Equal(t, configMap.Data["test-nginx.conf"], testFileRead(t, appPath+"/nginx.conf"))
	assert.Equal(t, configMap.Name, deployment.Spec.Template.Spec.Containers[0].EnvFrom[0].ConfigMapRef.Name)
	assert.Equal(t, configMap.Name, deployment.Spec.Template.Spec.Volumes[0].ConfigMap.Name)
}

func TestKustomizeConfigMapGeneratorFromEnv(t *testing.T) {
	appPath, destroyDataDir := testDataDir(t, "./testdata/"+generators)

	defer destroyDataDir()
	kustomize := NewKustomizeApp(appPath, git.NopCreds{}, "", "")
	kustomizeSource := v1alpha1.ApplicationSourceKustomize{
		ConfigMapGenerators: v1alpha1.KustomizeConfigMapGenerators{
			v1alpha1.KustomizeConfigMapGenerator{
				Name:     "test-generator",
				EnvFiles: []string{"./test.env"},
			},
		},
	}

	objs, _, err := kustomize.Build(&kustomizeSource, nil)
	assert.Nil(t, err)
	if err != nil {
		assert.Equal(t, len(objs), 2)
	}

	var (
		configMap  v1.ConfigMap
		deployment appsv1.Deployment
	)

	for _, obj := range objs {
		switch obj.GetKind() {
		case "ConfigMap":
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), &configMap)
			assert.NoError(t, err)
		case "Deployment":
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), &deployment)
			assert.NoError(t, err)
		}
	}

	assert.Equal(t, "test-generator-8bht68ghc4", configMap.Name)
	assert.Equal(t, configMap.Data["envKeyA"], "envValueA")
	assert.Equal(t, configMap.Data["envKeyB"], "envValueB")
	assert.Equal(t, configMap.Name, deployment.Spec.Template.Spec.Containers[0].EnvFrom[0].ConfigMapRef.Name)
	assert.Equal(t, configMap.Name, deployment.Spec.Template.Spec.Volumes[0].ConfigMap.Name)

}

func TestFindKustomization(t *testing.T) {
	testFindKustomization(t, kustomization1, "kustomization.yaml")
	testFindKustomization(t, kustomization2a, "kustomization.yml")
	testFindKustomization(t, kustomization2b, "Kustomization")
}

func testFindKustomization(t *testing.T, set string, expected string) {
	kustomization, err := (&kustomize{path: "testdata/" + set}).findKustomization()
	assert.Nil(t, err)
	assert.Equal(t, "testdata/"+set+"/"+expected, kustomization)
}

func TestIsKustomization(t *testing.T) {
	assert.True(t, IsKustomization("kustomization.yaml"))
	assert.True(t, IsKustomization("kustomization.yml"))
	assert.True(t, IsKustomization("Kustomization"))
	assert.False(t, IsKustomization("rubbish.yml"))
}

func TestParseKustomizeBuildOptions(t *testing.T) {
	built := parseKustomizeBuildOptions("guestbook", "-v 6 --logtostderr")
	assert.Equal(t, []string{"build", "guestbook", "-v", "6", "--logtostderr"}, built)
}

func TestVersion(t *testing.T) {
	ver, err := Version(false)
	assert.NoError(t, err)
	assert.NotEmpty(t, ver)
}

package externalsecret

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/go-logr/logr"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metavalidation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/testutil"
)

func assertSafePersistedFailure(t *testing.T, es *api.ExternalSecret, err error, canary string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a failure")
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), canary) {
		t.Error("public error exposes the canary")
	}
	c := fake.NewClientBuilder().WithScheme(testutil.NewTestScheme(t)).
		WithStatusSubresource(&api.ExternalSecret{}).WithObjects(es.DeepCopy()).Build()
	r := &ExternalSecretReconciler{Client: c, Ctx: context.Background(), Log: logr.Discard()}
	stored := &api.ExternalSecret{}
	key := client.ObjectKeyFromObject(es)
	if getErr := c.Get(r.Ctx, key, stored); getErr != nil {
		t.Fatal(getErr)
	}
	r.updateResourceManagementStatus(stored, "operation", err)
	if getErr := c.Get(r.Ctx, key, stored); getErr != nil {
		t.Fatal(getErr)
	}
	if len(stored.Status.DataSyncResults) != 1 || stored.Status.DataSyncResults[0].Status != "Failed" {
		t.Fatal("failure status was not persisted")
	}
	if reason := stored.Status.DataSyncResults[0].Reason; reason == "" || strings.Contains(reason, canary) {
		t.Error("persisted status must contain a safe, nonempty failure reason")
	}
}

func TestSecretSafetyTemplateErrors(t *testing.T) {
	const canary = "FAKE_TEMPLATE_SECRET_CANARY"
	data := map[string]interface{}{"password": canary}
	for _, tt := range []struct {
		name, source string
		sentinel     error
	}{
		{"range", `{{ range .password }}{{ . }}{{ end }}`, errTemplateExecution},
		{"fail", `{{ fail .password }}`, errTemplateExecution},
		{"parse", `{{ ` + canary + ` }}`, errTemplateParse},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, rawErr := template.New("").Funcs(sprig.TxtFuncMap()).Parse(tt.source)
			if rawErr == nil {
				rawErr = raw.Execute(&bytes.Buffer{}, data)
			}
			if rawErr == nil || !strings.Contains(rawErr.Error(), canary) {
				t.Fatal("fixture does not reproduce the original disclosure")
			}
			stp := NewSimpleTemplateProcessor(nil)
			out, err := stp.executeTemplate(tt.source, data)
			if len(out) != 0 || !stderrors.Is(err, tt.sentinel) {
				t.Fatal("error classification or failed-output semantics changed")
			}
			assertSafePersistedFailure(t, buildTestExternalSecret(nil), err, canary)
			es := buildTestExternalSecret(&api.ExternalSecretTemplate{Data: map[string]string{"output": tt.source}})
			result, processErr := stp.ProcessAllTemplates(context.Background(), es, map[string][]byte{"password": []byte(canary)})
			if result.Stats.DataTemplatesProcessed != 0 || strings.Contains(fmt.Sprint(result.Stats), canary) {
				t.Error("template stats expose input or count failed output as success")
			}
			if tt.sentinel == errTemplateParse {
				assertSafePersistedFailure(t, es, processErr, canary)
			} else if processErr != nil || len(result.Stats.Errors) != 1 {
				t.Error("nonfatal grading changed")
			}
			src := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "default"}, Data: map[string][]byte{"config": []byte(tt.source)}}
			stp = NewSimpleTemplateProcessor(newTemplateSourceClient(t, src))
			_, err = stp.processSecretTemplate(context.Background(), &api.TemplateRef{Name: "source", Items: []api.TemplateRefItem{{Key: "config"}}}, "default", data)
			assertSafePersistedFailure(t, es, err, canary)
		})
	}
}

func TestSecretSafetyBcryptCost(t *testing.T) {
	const canary = "71717171"
	if _, err := bcrypt.GenerateFromPassword([]byte("fake-password"), 71717171); err == nil || !strings.Contains(err.Error(), canary) {
		t.Fatal("fixture must reproduce cost disclosure in the bcrypt error")
	}
	logs := testutil.CaptureKlogOutput(t, func() {
		out, err := NewSimpleTemplateProcessor(nil).executeTemplate(`{{ bcrypt "fake-password" (int .pin) }}`, map[string]interface{}{"pin": canary})
		if err != nil || len(out) != 0 {
			t.Fatal("bcrypt failure fallback changed")
		}
	})
	if !strings.Contains(logs, "bcrypt hash generation failed") || strings.Contains(logs, canary) {
		t.Error("bcrypt log exposes Secret-derived cost")
	}
}

func TestSecretSafetyTemplateHelpers(t *testing.T) {
	const canary = "919191919191919191e999"
	payload := `{"value":` + canary + `}`
	var parsed interface{}
	if err := json.Unmarshal([]byte(payload), &parsed); err == nil || !strings.Contains(err.Error(), canary) {
		t.Fatal("fixture must expose the numeric token in the raw JSON error")
	}
	stp := NewSimpleTemplateProcessor(nil)
	for _, tt := range []struct{ name, source, want string }{
		{"jsonPath decode", `{{ jsonPath .payload "value" }}`, ""},
		{"merge base", `{{ mergeJson .payload "{}" }}`, payload},
		{"merge override", `{{ mergeJson "{}" .payload }}`, "{}"},
		{"missing key", `{{ jsonPath .map "missing" }}`, ""},
		{"dynamic path", `{{ jsonPath "{}" .path }}`, ""},
		{"array index", `{{ jsonPath "[]" .path }}`, ""},
		{"primitive path", `{{ jsonPath "1" .path }}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logs := testutil.CaptureKlogOutput(t, func() {
				out, err := stp.executeTemplate(tt.source, map[string]interface{}{"payload": payload, "map": `{"` + canary + `":1}`, "path": canary})
				if err != nil || string(out) != tt.want {
					t.Error("helper fallback semantics changed")
				}
			})
			if logs == "" || strings.Contains(logs, canary) {
				t.Error("helper must log a safe diagnostic without Secret-derived values or keys")
			}
		})
	}
}

func TestSecretSafetyDynamicTemplateKeys(t *testing.T) {
	const canary = "FAKE_DYNAMIC_KEY_CANARY!"
	for _, target := range []api.TemplateTarget{api.TemplateTargetData, api.TemplateTargetLabels, api.TemplateTargetAnnotations} {
		t.Run(string(target), func(t *testing.T) {
			src := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "default"}, Data: map[string][]byte{"config": []byte("password={{ .password }}")}}
			es := buildTestExternalSecret(&api.ExternalSecretTemplate{TemplateFrom: []api.TemplateFrom{{Target: target, Secret: &api.TemplateRef{Name: "source", Items: []api.TemplateRefItem{{Key: "config", TemplateAs: api.TemplateScopeKeysAndValues}}}}}})
			logs := testutil.CaptureKlogOutput(t, func() {
				_, err := NewSimpleTemplateProcessor(newTemplateSourceClient(t, src)).ProcessAllTemplates(context.Background(), es, map[string][]byte{"password": []byte("ok\n" + canary + "=x")})
				if err != nil {
					t.Fatal(err)
				}
			})
			if !strings.Contains(logs, "invalid") || strings.Contains(logs, canary) {
				t.Error("invalid dynamic key must be dropped without being logged")
			}
		})
	}
}

func TestSecretSafetyTemplateEnvironment(t *testing.T) {
	const name, canary = "ACK_TEST_FAKE_CREDENTIAL", "FAKE_ENV_SECRET_CANARY"
	t.Setenv(name, canary)
	for _, source := range []string{`{{ env "` + name + `" }}`, `{{ expandenv "$` + name + `" }}`} {
		raw := template.Must(template.New("").Funcs(sprig.TxtFuncMap()).Parse(source))
		var buf bytes.Buffer
		if err := raw.Execute(&buf, nil); err != nil || buf.String() != canary {
			t.Fatal("fixture must demonstrate access to the process environment")
		}
		out, err := NewSimpleTemplateProcessor(nil).executeTemplate(source, nil)
		if len(out) != 0 || !stderrors.Is(err, errTemplateParse) {
			t.Error("environment-reading template functions must be unavailable")
		}
	}
	if _, allowed := NewSimpleTemplateProcessor(nil).funcMap["getHostByName"]; allowed {
		t.Error("templates must not have a DNS exfiltration function")
	}
}

func TestSecretSafetyTemplateDNS(t *testing.T) {
	const canary = "fake-dns-secret-canary"
	var attempts atomic.Int32
	var disclosed atomic.Bool
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		attempts.Add(1)
		left, right := net.Pipe()
		go func() {
			defer right.Close()
			buf := make([]byte, 4096)
			n, _ := right.Read(buf)
			if bytes.Contains(buf[:n], []byte(canary)) {
				disclosed.Store(true)
			}
		}()
		return left, nil
	}}
	t.Cleanup(func() { net.DefaultResolver = previous })
	source := `{{ getHostByName .password }}`
	data := map[string]interface{}{"password": canary + ".invalid."}
	raw := template.Must(template.New("").Funcs(sprig.TxtFuncMap()).Parse(source))
	_ = raw.Execute(&bytes.Buffer{}, data)
	if attempts.Load() == 0 || !disclosed.Load() {
		t.Fatal("fixture must put the canary in a DNS query captured locally")
	}
	attempts.Store(0)
	_, err := NewSimpleTemplateProcessor(nil).executeTemplate(source, data)
	if !stderrors.Is(err, errTemplateParse) || attempts.Load() != 0 {
		t.Fatal("restricted function must fail before any DNS request")
	}
}

func TestSecretSafetyExtractStatus(t *testing.T) {
	_, err := common.ProcessExtractedExternalSecretData(&api.DataProcess{Extract: &api.DataSource{Key: "source"}}, []byte("FAKE_PASS"))
	es := buildTestExternalSecret(nil)
	assertSafePersistedFailure(t, es, err, "FAKE_PASS")
	c := fake.NewClientBuilder().WithScheme(testutil.NewTestScheme(t)).WithStatusSubresource(&api.ExternalSecret{}).WithObjects(es).Build()
	r := &ExternalSecretReconciler{Client: c, Ctx: context.Background()}
	r.updateExternalSecretStatus(es, nil, map[string]error{"source": err}, nil, nil, nil, true, true)
	stored := &api.ExternalSecret{}
	if getErr := c.Get(r.Ctx, client.ObjectKeyFromObject(es), stored); getErr != nil {
		t.Fatal(getErr)
	}
	if len(stored.Status.DataSyncResults) != 1 || stored.Status.DataSyncResults[0].Status != "Failed" || strings.Contains(stored.Status.DataSyncResults[0].Reason, "FAKE_") {
		t.Fatal("extract failure must persist without scalar disclosure")
	}
}

func TestSecretSafetyAPIValidation(t *testing.T) {
	const canary = "FAKE_LABEL_SECRET_CANARY!"
	for _, update := range []bool{false, true} {
		t.Run(fmt.Sprintf("update=%v", update), func(t *testing.T) {
			es := buildTestExternalSecret(nil)
			calls := 0
			validate := func(obj client.Object) error {
				calls++
				problems := metavalidation.ValidateLabels(obj.GetLabels(), field.NewPath("metadata", "labels"))
				err := apierrors.NewInvalid(schema.GroupKind{Kind: "Secret"}, obj.GetName(), problems)
				if len(problems) == 0 || !strings.Contains(err.Error(), canary) {
					t.Fatal("validation fixture must disclose BadValue")
				}
				return err
			}
			builder := fake.NewClientBuilder().WithScheme(testutil.NewTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
				Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
					return validate(obj)
				},
				Update: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.UpdateOption) error {
					return validate(obj)
				},
			})
			if update {
				builder.WithObjects(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: es.Spec.Target.Name, Namespace: es.Namespace}})
			}
			handler := NewSimpleSecretOperationHandler(builder.Build(), false, logr.Discard())
			err := handler.HandleSecretOperation(context.Background(), es, map[string][]byte{"password": []byte(canary)}, nil, map[string]map[string]string{"labels": {"value": canary}})
			if calls != 1 || !apierrors.IsInvalid(err) {
				t.Fatal("API error classification or write behavior changed")
			}
			assertSafePersistedFailure(t, es, err, canary)
		})
	}
}

type secretSafetyWriteClient struct {
	client.Client
	target client.ObjectKey
	writes [3]int
	reject func(*corev1.Secret) error
}

func (c *secretSafetyWriteClient) attempt(obj client.Object, operation int) error {
	if secret, ok := obj.(*corev1.Secret); ok && client.ObjectKeyFromObject(secret) == c.target {
		c.writes[operation]++
		if c.reject != nil && operation < 2 {
			return c.reject(secret)
		}
	}
	return nil
}

func (c *secretSafetyWriteClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if err := c.attempt(obj, 0); err != nil {
		return err
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c *secretSafetyWriteClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if err := c.attempt(obj, 1); err != nil {
		return err
	}
	return c.Client.Update(ctx, obj, opts...)
}

func (c *secretSafetyWriteClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if err := c.attempt(obj, 2); err != nil {
		return err
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func TestSecretSafetyRestrictedFunctionsReconcile(t *testing.T) {
	const canary = "FAKE_TEMPLATE_UPGRADE_CANARY"
	for _, function := range []string{"env", "expandenv", "getHostByName"} {
		for _, location := range []string{"data", "labels", "annotations", "configmap-Data", "configmap-Labels", "configmap-Annotations", "secret-Data", "secret-Labels", "secret-Annotations", "literal-Data", "literal-Labels", "literal-Annotations"} {
			for _, policy := range []api.TemplateMergePolicy{api.MergePolicyReplace, api.MergePolicyMerge} {
				for _, existing := range []bool{false, true} {
					for _, cleanup := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/%s/%s/existing=%t/cleanup=%t", function, location, policy, existing, cleanup), func(t *testing.T) {
							tmpl := &api.ExternalSecretTemplate{
								MergePolicy: policy, Data: map[string]string{"output": "{{ .token }}"},
								Metadata: &api.ExternalSecretTemplateMetadata{Labels: map[string]string{"healthy": "ok"}, Annotations: map[string]string{"healthy": "ok"}},
							}
							disabled := fmt.Sprintf("{{ %s %q }}", function, canary)
							var source client.Object
							configure := func(tmpl *api.ExternalSecretTemplate, text string) {
								switch location {
								case "data":
									tmpl.Data["output"] = text
								case "labels":
									tmpl.Metadata.Labels["blocked"] = text
								case "annotations":
									tmpl.Metadata.Annotations["blocked"] = text
								default:
									parts := strings.SplitN(location, "-", 2)
									ref := &api.TemplateRef{Name: "template-source", Items: []api.TemplateRefItem{{Key: "rendered", TemplateAs: api.TemplateScopeValues}}}
									from := api.TemplateFrom{Target: api.TemplateTarget(parts[1])}
									meta := metav1.ObjectMeta{Name: ref.Name, Namespace: "default"}
									switch parts[0] {
									case "configmap":
										from.ConfigMap = ref
										source = &corev1.ConfigMap{ObjectMeta: meta, Data: map[string]string{"rendered": text}}
									case "secret":
										from.Secret = ref
										source = &corev1.Secret{ObjectMeta: meta, Data: map[string][]byte{"rendered": []byte(text)}}
									case "literal":
										from.Literal = &text
									}
									tmpl.TemplateFrom = []api.TemplateFrom{from}
								}
							}
							configure(tmpl, disabled)
							es := buildTestExternalSecret(tmpl)
							es.Finalizers = []string{secretFinalizer}
							es.Spec.Provider = "kms"
							es.Spec.Data = []api.DataSource{{Key: "token"}}
							objs := []client.Object{es, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: es.Namespace}}}
							if source != nil {
								objs = append(objs, source)
							}
							old := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: es.Spec.Target.Name, Namespace: es.Namespace, Labels: map[string]string{"blocked": "old", "healthy": "old"}, Annotations: map[string]string{"blocked": "old", "healthy": "old"}}, Data: map[string][]byte{"output": []byte("old"), "legacy": []byte("keep-on-failure")}}
							if existing {
								objs = append(objs, old)
							}
							sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{"token": {"token": []byte(canary)}}}
							r := newTestReconciler(t, cleanup, sc, objs...)
							writes := &secretSafetyWriteClient{Client: r.Client, target: client.ObjectKeyFromObject(old)}
							r.Client = writes
							var logs bytes.Buffer
							r.Log = zap.New(zap.WriteTo(&logs))
							req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(es)}
							fatal := location != "labels" && location != "annotations"
							_, err := r.Reconcile(context.Background(), req)
							if (err != nil) != fatal {
								t.Fatalf("fatal grading changed: %v", err)
							}
							got := getTestSecret(t, r, es.Namespace, es.Spec.Target.Name)
							statusKey := "template_processing_warnings"
							if fatal {
								statusKey = "template_processing_fatal"
								if writes.writes != [3]int{} || (got != nil) != existing {
									t.Fatal("fatal parse error must not create, update or delete the Secret")
								}
								if existing && (!reflect.DeepEqual(got.Data, old.Data) || !reflect.DeepEqual(got.Labels, old.Labels) || !reflect.DeepEqual(got.Annotations, old.Annotations)) {
									t.Fatal("fatal parse error changed the existing Secret")
								}
							} else {
								wantWrites := [3]int{1, 0, 0}
								if existing {
									wantWrites = [3]int{0, 1, 0}
								}
								if got == nil || writes.writes != wantWrites || string(got.Data["output"]) != canary || got.Labels["healthy"] != "ok" || got.Annotations["healthy"] != "ok" {
									t.Fatal("inline metadata failure must allow successful data and metadata writes")
								}
								values := got.Labels
								if location == "annotations" {
									values = got.Annotations
								}
								value, present := values["blocked"]
								if present != existing || (existing && value != "old") {
									t.Fatal("failed metadata must retain old values or remain absent on creation")
								}
							}
							stored := &api.ExternalSecret{}
							if getErr := r.Get(r.Ctx, req.NamespacedName, stored); getErr != nil {
								t.Fatal(getErr)
							}
							found := false
							for _, result := range stored.Status.DataSyncResults {
								if result.ExternalSecretKey == statusKey && result.Status == "Failed" && strings.Contains(result.Reason, "failed to parse template") {
									found = true
								}
							}
							if !found || strings.Contains(fmt.Sprint(stored.Status, err, logs.String()), canary) {
								t.Fatal("reconcile must persist safe parse diagnostics")
							}
							configure(stored.Spec.Target.Template, "{{ .token }}")
							if source != nil {
								current := source.DeepCopyObject().(client.Object)
								if err := r.Get(r.Ctx, client.ObjectKeyFromObject(source), current); err != nil {
									t.Fatal(err)
								}
								source.SetResourceVersion(current.GetResourceVersion())
								if err := r.Update(r.Ctx, source); err != nil {
									t.Fatal(err)
								}
							}
							if err := r.Update(r.Ctx, stored); err != nil {
								t.Fatal(err)
							}
							writes.writes = [3]int{}
							if _, err := r.Reconcile(context.Background(), req); err != nil {
								t.Fatal(err)
							}
							got = getTestSecret(t, r, es.Namespace, es.Spec.Target.Name)
							wantData := map[string][]byte{"output": []byte(canary)}
							if policy == api.MergePolicyMerge {
								wantData["token"] = []byte(canary)
							}
							if strings.HasSuffix(location, "-Data") {
								key := "rendered"
								if strings.HasPrefix(location, "literal-") {
									key = "literal"
								}
								wantData[key] = []byte(canary)
							}
							if got == nil || !reflect.DeepEqual(got.Data, wantData) || writes.writes[0]+writes.writes[1] != 1 || writes.writes[2] != 0 {
								t.Fatal("explicit data-source migration must restore writes with the original merge policy")
							}
							if err := r.Get(r.Ctx, req.NamespacedName, stored); err != nil {
								t.Fatal(err)
							}
							if len(stored.Status.DataSyncResults) == 0 {
								t.Fatal("recovery must persist successful synchronization")
							}
							for _, result := range stored.Status.DataSyncResults {
								if result.Status != "Succeeded" {
									t.Fatal("recovery must clear the previous template failure")
								}
							}
						})
					}
				}
			}
		}
	}
}

func TestSecretSafetyAPIReconcileDiagnostics(t *testing.T) {
	const canary = "FAKE_API_SECRET_CANARY!"
	for _, category := range []string{"invalid", "forbidden", "conflict"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", category, existing), func(t *testing.T) {
				es := buildTestExternalSecret(&api.ExternalSecretTemplate{Metadata: &api.ExternalSecretTemplateMetadata{Labels: map[string]string{"value": "{{ .token }}"}}})
				es.Finalizers = []string{secretFinalizer}
				es.Spec.Provider = "kms"
				es.Spec.Data = []api.DataSource{{Key: "token"}}
				objs := []client.Object{es, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: es.Namespace}}}
				old := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: es.Spec.Target.Name, Namespace: es.Namespace}, Data: map[string][]byte{"token": []byte("old")}}
				if existing {
					objs = append(objs, old)
				}
				r := newTestReconciler(t, false, &fakeSecretClient{dataByKey: map[string]map[string][]byte{"token": {"token": []byte(canary)}}}, objs...)
				var raw error
				writes := &secretSafetyWriteClient{Client: r.Client, target: client.ObjectKeyFromObject(old), reject: func(secret *corev1.Secret) error {
					switch category {
					case "invalid":
						problems := metavalidation.ValidateLabels(secret.Labels, field.NewPath("metadata", "labels"))
						raw = apierrors.NewInvalid(schema.GroupKind{Kind: "Secret"}, secret.Name, problems)
					case "forbidden":
						raw = apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, secret.Name, stderrors.New(canary))
					case "conflict":
						raw = apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, secret.Name, stderrors.New(canary))
					}
					if !strings.Contains(raw.Error(), canary) {
						t.Fatal("raw API fixture must contain the canary")
					}
					return raw
				}}
				r.Client = writes
				var logs bytes.Buffer
				r.Log = zap.New(zap.WriteTo(&logs))
				req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(es)}
				_, err := r.Reconcile(context.Background(), req)
				if err == nil || raw == nil || !stderrors.Is(err, raw) || apierrors.ReasonForError(err) != apierrors.ReasonForError(raw) {
					t.Fatal("reconcile must preserve the API failure classification")
				}
				stored := &api.ExternalSecret{}
				if err := r.Get(r.Ctx, req.NamespacedName, stored); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, result := range stored.Status.DataSyncResults {
					if result.ExternalSecretKey == "update_operation" && result.Status == "Failed" && strings.HasSuffix(result.Reason, ": "+category) {
						found = true
					}
				}
				if !found || !strings.Contains(logs.String(), ": "+category) || strings.Contains(fmt.Sprint(stored.Status, err, logs.String()), canary) {
					t.Fatal("actual reconcile logs and status must preserve only safe diagnostics")
				}
				got := getTestSecret(t, r, es.Namespace, es.Spec.Target.Name)
				wantWrites := [3]int{1, 0, 0}
				if existing {
					wantWrites = [3]int{0, 1, 0}
				}
				if writes.writes != wantWrites || (got != nil) != existing || (existing && !reflect.DeepEqual(got.Data, old.Data)) {
					t.Fatal("failed API write must not replace existing data or delete the Secret")
				}
			})
		}
	}
}

// TestBcryptFunction tests the bcrypt password hashing function
func TestBcryptFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		password  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:      "bcrypt with default cost",
			template:  `{{ bcrypt "password123" }}`,
			password:  "password123",
			data:      map[string]string{},
			expected:  "$2a$10$", // bcrypt prefix with default cost 10
			expectErr: false,
		},
		{
			name:      "bcrypt with custom cost",
			template:  `{{ bcrypt "password123" 12 }}`,
			password:  "password123",
			data:      map[string]string{},
			expected:  "$2a$12$", // bcrypt prefix with cost 12
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}

			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if !tt.expectErr && !strings.HasPrefix(string(result), tt.expected) {
				t.Errorf("Expected bcrypt hash starting with %q, got %q", tt.expected, string(result))
			}
			// Verify the hash actually round-trips with the input password.
			if !tt.expectErr {
				if err := bcrypt.CompareHashAndPassword(result, []byte(tt.password)); err != nil {
					t.Errorf("bcrypt hash does not verify against input password %q: %v", tt.password, err)
				}
			}
		})
	}
}

// TestHtpasswdFunction tests the htpasswd format generation
func TestHtpasswdFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expectErr bool
	}{
		{
			name:      "htpasswd basic auth",
			template:  `{{ htpasswd "admin" "password123" }}`,
			data:      map[string]string{},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if !tt.expectErr {
				output := string(result)
				if !strings.HasPrefix(output, "admin:") {
					t.Errorf("Expected htpasswd format starting with 'admin:', got %q", output)
					return
				}
				hashPart := strings.TrimPrefix(output, "admin:")
				if !strings.HasPrefix(hashPart, "$2") {
					t.Errorf("Expected bcrypt hash (starting with $2) after username, got %q", hashPart)
					return
				}
				// Verify the bcrypt hash round-trips against the input password.
				if err := bcrypt.CompareHashAndPassword([]byte(hashPart), []byte("password123")); err != nil {
					t.Errorf("htpasswd bcrypt hash does not verify against input password: %v", err)
				}
			}
		})
	}
}

// TestJsonPathFunction tests the JSON path extraction function
func TestJsonPathFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "jsonPath simple nested access",
			template: `{{ jsonPath .config "database.host" }}`,
			data: map[string]string{
				"config": `{"database": {"host": "localhost", "port": 5432}}`,
			},
			expected:  "localhost",
			expectErr: false,
		},
		{
			name:     "jsonPath array access",
			template: `{{ jsonPath .users "0.name" }}`,
			data: map[string]string{
				"users": `[{"name": "alice"}, {"name": "bob"}]`,
			},
			expected:  "alice",
			expectErr: false,
		},
		{
			name:     "jsonPath deep nested",
			template: `{{ jsonPath .data "a.b.c" }}`,
			data: map[string]string{
				"data": `{"a": {"b": {"c": "deep_value"}}}`,
			},
			expected:  "deep_value",
			expectErr: false,
		},
		{
			name:     "jsonPath invalid path",
			template: `{{ jsonPath .data "nonexistent" }}`,
			data: map[string]string{
				"data": `{"key": "value"}`,
			},
			expected:  "",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestJsonPathWithNestedAccess tests jsonPath function with nested object access
func TestJsonPathWithNestedAccess(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "jsonPath nested access with double quotes",
			template: `{{ jsonPath .jsonData "json_object.nested.inner" }}`,
			data: map[string]string{
				"jsonData": `{"json_object": {"nested": {"inner": "deep-value"}}}`,
			},
			expected:  "deep-value",
			expectErr: false,
		},
		{
			name:     "jsonPath array and nested access",
			template: `{{ jsonPath .data "users.0.address.city" }}`,
			data: map[string]string{
				"data": `{"users": [{"name": "alice", "address": {"city": "Beijing"}}]}`,
			},
			expected:  "Beijing",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}

			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestMergeJsonFunction tests the JSON merge function
func TestMergeJsonFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "mergeJson combines two objects",
			template: `{{ mergeJson .base .override }}`,
			data: map[string]string{
				"base":     `{"a": 1, "b": 2}`,
				"override": `{"b": 3, "c": 4}`,
			},
			expected:  `{"a":1,"b":3,"c":4}`,
			expectErr: false,
		},
		{
			name:     "mergeJson deep merge",
			template: `{{ mergeJson .base .override }}`,
			data: map[string]string{
				"base":     `{"config": {"a": 1, "b": 2}}`,
				"override": `{"config": {"b": 3}}`,
			},
			expected:  `{"config":{"a":1,"b":3}}`,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestParseKeyValueFunction tests the key=value parsing function
func TestParseKeyValueFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "parseKeyValue basic",
			template: `{{ $kv := parseKeyValue .data }}{{ index $kv "KEY1" }}`,
			data: map[string]string{
				"data": "KEY1=value1\nKEY2=value2",
			},
			expected:  "value1",
			expectErr: false,
		},
		{
			name:     "parseKeyValue with comments",
			template: `{{ $kv := parseKeyValue .data }}{{ index $kv "KEY" }}`,
			data: map[string]string{
				"data": "# Comment\nKEY=value\n# Another comment",
			},
			expected:  "value",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestExecuteTemplateWithSprigFunctions tests standard Go template syntax
// combined with Sprig functions still works as expected.
func TestExecuteTemplateWithSprigFunctions(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "direct field access",
			template: `{{ .username }}`,
			data: map[string]string{
				"username": "alice",
			},
			expected:  "alice",
			expectErr: false,
		},
		{
			name:     "range over data",
			template: `{{ range $k, $v := . }}{{ $k }}={{ $v }};{{ end }}`,
			data: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			expected:  "", // order-independent; validated below
			expectErr: false,
		},
		{
			name:     "if condition",
			template: `{{ if eq .status "active" }}yes{{ else }}no{{ end }}`,
			data: map[string]string{
				"status": "active",
			},
			expected:  "yes",
			expectErr: false,
		},
		{
			name:     "pipe with Sprig function",
			template: `{{ .name | upper }}`,
			data: map[string]string{
				"name": "alice",
			},
			expected:  "ALICE",
			expectErr: false,
		},
		{
			name:     "fromJson with index",
			template: `{{ index (.config | fromJson) "host" }}`,
			data: map[string]string{
				"config": `{"host": "localhost", "port": 5432}`,
			},
			expected:  "localhost",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			// The "range over data" case is order-independent due to Go map
			// iteration order being non-deterministic.
			if tt.name == "range over data" {
				output := string(result)
				parts := strings.Split(strings.TrimSuffix(output, ";"), ";")
				sort.Strings(parts)
				expectedParts := []string{"key1=value1", "key2=value2"}
				if len(parts) != len(expectedParts) {
					t.Errorf("Expected %d key=value pairs, got %d: %q", len(expectedParts), len(parts), output)
					return
				}
				for i, p := range parts {
					if p != expectedParts[i] {
						t.Errorf("Expected sorted pair[%d]=%q, got %q (full output=%q)", i, expectedParts[i], p, output)
					}
				}
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Error grading (parse vs execution, fatal vs non-fatal)
//
// These exercise the template processor in isolation. They complement -- and
// deliberately do NOT duplicate -- the reconcile-level fatal-template contract
// in write_decision_contract_test.go, which drives the full syncIfNeedUpdate
// path. Here the focus is executeTemplate's sentinel grading and how it
// surfaces through ProcessAllTemplates' FatalErrors vs Stats.Errors buckets.
// ---------------------------------------------------------------------------

// TestExecuteTemplateErrorGrading pins the sentinel-based grading of
// executeTemplate: a structurally invalid template wraps errTemplateParse
// (fatal) while a runtime failure wraps errTemplateExecution (non-fatal).
func TestExecuteTemplateErrorGrading(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)
	data := map[string]interface{}{"a": "x"}

	tests := []struct {
		name      string
		template  string
		wantParse bool
		wantExec  bool
	}{
		{
			name:      "unclosed action is a parse error (fatal)",
			template:  "{{ .a",
			wantParse: true,
		},
		{
			name:      "missing if operand is a parse error (fatal)",
			template:  "{{ if }}x{{ end }}",
			wantParse: true,
		},
		{
			name:     "field access on a string value is an execution error (non-fatal)",
			template: "{{ .a.b }}",
			wantExec: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := stp.executeTemplate(tt.template, data)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if gotParse := stderrors.Is(err, errTemplateParse); gotParse != tt.wantParse {
				t.Errorf("errTemplateParse match = %v, want %v (err=%v)", gotParse, tt.wantParse, err)
			}
			if gotExec := stderrors.Is(err, errTemplateExecution); gotExec != tt.wantExec {
				t.Errorf("errTemplateExecution match = %v, want %v (err=%v)", gotExec, tt.wantExec, err)
			}
		})
	}
}

// TestProcessAllTemplatesErrorGrading pins how executeTemplate's graded errors
// surface through ProcessAllTemplates: a parse error in an inline Template.Data
// entry becomes a FatalError that aborts the round (returned error), while an
// execution error is collected as a non-fatal Stats.Errors entry and the round
// still succeeds with that key skipped.
func TestProcessAllTemplatesErrorGrading(t *testing.T) {
	t.Run("parse error is fatal and aborts the round", func(t *testing.T) {
		stp := NewSimpleTemplateProcessor(nil)
		es := buildTestExternalSecret(&api.ExternalSecretTemplate{
			Data: map[string]string{"out": "{{ if }}"},
		})
		result, err := stp.ProcessAllTemplates(context.Background(), es, map[string][]byte{"a": []byte("x")})
		if err == nil {
			t.Fatalf("expected a fatal error for a parse failure, got nil")
		}
		if len(result.Stats.FatalErrors) == 0 {
			t.Fatalf("expected FatalErrors to be populated, got %+v", result.Stats)
		}
		if len(result.Stats.Errors) != 0 {
			t.Fatalf("a parse failure must not be graded as a non-fatal error, got %+v", result.Stats.Errors)
		}
	})

	t.Run("execution error is non-fatal and the round succeeds", func(t *testing.T) {
		stp := NewSimpleTemplateProcessor(nil)
		es := buildTestExternalSecret(&api.ExternalSecretTemplate{
			Data: map[string]string{
				"good": "{{ .a }}",
				"bad":  "{{ .a.b }}", // field access on a string -> execution error
			},
		})
		result, err := stp.ProcessAllTemplates(context.Background(), es, map[string][]byte{"a": []byte("x")})
		if err != nil {
			t.Fatalf("execution errors must be non-fatal, got %v", err)
		}
		if len(result.Stats.FatalErrors) != 0 {
			t.Fatalf("an execution error must not be graded fatal, got %+v", result.Stats.FatalErrors)
		}
		if len(result.Stats.Errors) == 0 {
			t.Fatalf("expected a non-fatal Stats.Errors entry for the failed key, got %+v", result.Stats)
		}
		// The good key still renders; the failed-execution key is skipped.
		if string(result.Data["good"]) != "x" {
			t.Errorf("expected the good key to render as %q, got %q", "x", string(result.Data["good"]))
		}
		if _, ok := result.Data["bad"]; ok {
			t.Errorf("the failed-execution key must be skipped, got %v", result.Data)
		}
	})
}

// ---------------------------------------------------------------------------
// templateFrom Secret source (processSecretTemplate)
//
// Covers the Secret-backed templateFrom source: the template body is read
// from a referenced Secret's data key, rendered against the fetched data and
// placed under the item key (Values scope) or parsed into pairs
// (KeysAndValues scope).
// ---------------------------------------------------------------------------

// newTemplateSourceClient builds a fake client seeded with templateFrom
// source objects (core scheme only is sufficient).
func newTemplateSourceClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// TestProcessSecretTemplateFromSecretRef drives processSecretTemplate directly
// against a fake client holding the template source Secret.
func TestProcessSecretTemplateFromSecretRef(t *testing.T) {
	const ns = "default"
	srcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tpl-src", Namespace: ns},
		Data: map[string][]byte{
			"config.tpl": []byte("user={{ .username }}"),
			"env.tpl":    []byte("DB_HOST={{ .host }}\nDB_PORT=3306"),
			"broken.tpl": []byte("{{ .unclosed"),
		},
	}

	tests := []struct {
		name      string
		ref       *api.TemplateRef
		data      map[string]interface{}
		expected  map[string]string
		errSubstr string
	}{
		{
			name: "renders template read from Secret with context substitution",
			ref: &api.TemplateRef{
				Name:  "tpl-src",
				Items: []api.TemplateRefItem{{Key: "config.tpl"}},
			},
			data:     map[string]interface{}{"username": "alice"},
			expected: map[string]string{"config.tpl": "user=alice"},
		},
		{
			name: "explicit Values scope behaves like the default",
			ref: &api.TemplateRef{
				Name:  "tpl-src",
				Items: []api.TemplateRefItem{{Key: "config.tpl", TemplateAs: api.TemplateScopeValues}},
			},
			data:     map[string]interface{}{"username": "bob"},
			expected: map[string]string{"config.tpl": "user=bob"},
		},
		{
			name: "KeysAndValues scope parses rendered key=value lines",
			ref: &api.TemplateRef{
				Name:  "tpl-src",
				Items: []api.TemplateRefItem{{Key: "env.tpl", TemplateAs: api.TemplateScopeKeysAndValues}},
			},
			data: map[string]interface{}{"host": "db.local"},
			expected: map[string]string{
				"DB_HOST": "db.local",
				"DB_PORT": "3306",
			},
		},
		{
			name: "referenced Secret does not exist",
			ref: &api.TemplateRef{
				Name:  "missing-tpl-src",
				Items: []api.TemplateRefItem{{Key: "config.tpl"}},
			},
			data:      map[string]interface{}{},
			errSubstr: "failed to get Secret missing-tpl-src",
		},
		{
			name: "key absent from the referenced Secret",
			ref: &api.TemplateRef{
				Name:  "tpl-src",
				Items: []api.TemplateRefItem{{Key: "absent.tpl"}},
			},
			data:      map[string]interface{}{},
			errSubstr: "key absent.tpl not found in Secret tpl-src",
		},
		{
			name: "structurally invalid template fails to parse",
			ref: &api.TemplateRef{
				Name:  "tpl-src",
				Items: []api.TemplateRefItem{{Key: "broken.tpl"}},
			},
			data:      map[string]interface{}{},
			errSubstr: "failed to process Secret template key broken.tpl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stp := NewSimpleTemplateProcessor(newTemplateSourceClient(t, srcSecret))
			got, err := stp.processSecretTemplate(context.Background(), tt.ref, ns, tt.data)
			if tt.errSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (result=%v)", tt.errSubstr, got)
				}
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("expected error containing %q, got %v", tt.errSubstr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.expected) {
				t.Fatalf("expected %d rendered keys, got %v", len(tt.expected), got)
			}
			for k, want := range tt.expected {
				if string(got[k]) != want {
					t.Errorf("key %q: expected %q, got %q", k, want, string(got[k]))
				}
			}
		})
	}
}

// TestProcessAllTemplatesWithSecretTemplateFrom pins the end-to-end path:
// ProcessAllTemplates resolves a Secret-backed templateFrom entry into the
// Data target and records the rendered key in the managed-keys annotation.
func TestProcessAllTemplatesWithSecretTemplateFrom(t *testing.T) {
	const ns = "default"
	srcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tpl-src", Namespace: ns},
		Data: map[string][]byte{
			"config.tpl": []byte("user={{ .username }}"),
		},
	}
	stp := NewSimpleTemplateProcessor(newTemplateSourceClient(t, srcSecret))
	es := buildTestExternalSecret(&api.ExternalSecretTemplate{
		TemplateFrom: []api.TemplateFrom{
			{
				Secret: &api.TemplateRef{
					Name:  "tpl-src",
					Items: []api.TemplateRefItem{{Key: "config.tpl"}},
				},
				Target: api.TemplateTargetData,
			},
		},
	})

	result, err := stp.ProcessAllTemplates(context.Background(), es, map[string][]byte{"username": []byte("alice")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(result.Data["config.tpl"]); got != "user=alice" {
		t.Fatalf("expected rendered config.tpl=user=alice, got %v", result.Data)
	}
	// Replace mode with a Data-targeted templateFrom clears the raw data.
	if _, ok := result.Data["username"]; ok {
		t.Fatalf("raw data must be cleared in Replace mode, got %v", result.Data)
	}
	if got := result.Metadata.Annotations[ManagedKeysAnnotation]; got != "config.tpl" {
		t.Fatalf("expected managed-keys annotation config.tpl, got %q", got)
	}
	if result.Stats.TemplateFromProcessed != 1 {
		t.Fatalf("expected 1 templateFrom source processed, got %d", result.Stats.TemplateFromProcessed)
	}
}

// TestIsValidLabelKey: label keys follow the Kubernetes qualified-name syntax
// enforced by the API server, so mixed case, '_' and '.' are accepted in the
// name segment while the prefix must be a lowercase DNS subdomain.
func TestIsValidLabelKey(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		key  string
		want bool
	}{
		{"app", true},
		{"app.kubernetes.io/name", true},
		{"istio/multiCluster", true},
		{"example.com/Mixed_Case.name-1", true},
		{"A", true},
		{"", false},
		{"/name", false},
		{"prefix/", false},
		{"a/b/c", false},
		{"-name", false},
		{"name-", false},
		{"_name", false},
		{"Example.com/name", false},
		{"example.com/na me", false},
		{"example.com/" + strings.Repeat("a", 64), false},
		{"example.com/é", false},
	}

	for _, tt := range tests {
		if got := stp.isValidLabelKey(tt.key); got != tt.want {
			t.Errorf("isValidLabelKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

// TestIsValidAnnotationKey: the Kubernetes API server validates annotation
// keys as case-insensitive qualified names (strings.ToLower before
// IsQualifiedName), so uppercase prefixes and names are accepted.
func TestIsValidAnnotationKey(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		key  string
		want bool
	}{
		{"app", true},
		{"app.kubernetes.io/name", true},
		{"Example.com/name", true},
		{"EXAMPLE.COM/NAME", true},
		{"example.com/Mixed_Case.name-1", true},
		{"", false},
		{"/name", false},
		{"prefix/", false},
		{"a/b/c", false},
		{"example.com/na me", false},
		{"example.com/" + strings.Repeat("a", 64), false},
		{"example.com/é", false},
	}

	for _, tt := range tests {
		if got := stp.isValidAnnotationKey(tt.key); got != tt.want {
			t.Errorf("isValidAnnotationKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

// TestIsValidSecretKey: secret data keys follow the Kubernetes IsConfigMapKey
// rules, so keys may start and end with '-', '_' or '.' and must stay within
// 253 characters without a ".." prefix.
func TestIsValidSecretKey(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		key  string
		want bool
	}{
		{"key", true},
		{"key.name_1-x", true},
		{".dotfile", true},
		{"trailing.", true},
		{"_underscore", true},
		{"-dash", true},
		{"", false},
		{"..dotdot", false},
		{"key name", false},
		{"key/name", false},
		{"café", false},
		{strings.Repeat("a", 254), false},
	}

	for _, tt := range tests {
		if got := stp.isValidSecretKey(tt.key); got != tt.want {
			t.Errorf("isValidSecretKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

// TestInlineTemplateDataKeyDropFeedback pins that dropping an invalid inline
// data key stays non-fatal AND is recorded in Stats.Errors (the channel the
// controller persists as template_processing_warnings), so the drop is
// visible in the ExternalSecret status instead of only controller logs.
func TestInlineTemplateDataKeyDropFeedback(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)
	es := buildTestExternalSecret(&api.ExternalSecretTemplate{
		Data: map[string]string{
			"good":    "{{ .raw }}",
			"bad key": "static", // invalid key with a valid template: dropped before rendering
		},
	})
	result, err := stp.ProcessAllTemplates(context.Background(), es, map[string][]byte{"raw": []byte("v")})
	if err != nil {
		t.Fatalf("invalid-key drop must stay non-fatal, got %v", err)
	}
	if string(result.Data["good"]) != "v" {
		t.Fatalf("valid key must still render, got %v", result.Data)
	}
	if _, ok := result.Data["bad key"]; ok {
		t.Fatal("invalid key must not be written")
	}
	if len(result.Stats.Errors) != 1 || !strings.Contains(result.Stats.Errors[0], "bad key") {
		t.Fatalf("drop must be recorded in Stats.Errors with the key name, got %+v", result.Stats.Errors)
	}
}

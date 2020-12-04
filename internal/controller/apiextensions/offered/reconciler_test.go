/*
Copyright 2020 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package offered

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/resource/fake"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/crossplane/crossplane/apis/apiextensions/v1beta1"
)

type MockEngine struct {
	ControllerEngine
	MockStart func(name string, o kcontroller.Options, w ...controller.Watch) error
	MockStop  func(name string)
	MockErr   func(name string) error
}

func (m *MockEngine) Start(name string, o kcontroller.Options, w ...controller.Watch) error {
	return m.MockStart(name, o, w...)
}

func (m *MockEngine) Stop(name string) {
	m.MockStop(name)
}

func (m *MockEngine) Err(name string) error {
	return m.MockErr(name)
}

func TestReconcile(t *testing.T) {
	errBoom := errors.New("boom")
	now := metav1.Now()
	owner := types.UID("definitely-a-uuid")
	ctrlr := true

	type args struct {
		mgr  manager.Manager
		opts []ReconcilerOption
	}
	type want struct {
		r   reconcile.Result
		err error
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
	}{
		"CompositeResourceDefinitionNotFound": {
			reason: "We should not return an error if the CompositeResourceDefinition was not found.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(kerrors.NewNotFound(schema.GroupResource{}, "")),
						},
					}),
				},
			},
			want: want{
				r: reconcile.Result{},
			},
		},
		"GetCompositeResourceDefinitionError": {
			reason: "We should return any other error encountered while getting an CompositeResourceDefinition.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(errBoom),
						},
					}),
				},
			},
			want: want{
				err: errors.Wrap(errBoom, errGetXRD),
			},
		},
		"RenderCompositeResourceDefinitionError": {
			reason: "We should requeue after a short wait if we encounter an error while rendering a CRD.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil),
						},
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return nil, errBoom
					})),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"SetTerminatingConditionError": {
			reason: "We should requeue after a short wait if we encounter an error while setting the terminating status condition.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil, func(o runtime.Object) error {
								d := o.(*v1beta1.CompositeResourceDefinition)
								d.SetDeletionTimestamp(&now)
								return nil
							}),
							MockStatusUpdate: test.NewMockStatusUpdateFn(errBoom),
						},
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"GetCustomResourceDefinitionError": {
			reason: "We should requeue after a short wait if we encounter an error while getting a CRD.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil, func(o runtime.Object) error {
								switch v := o.(type) {
								case *v1beta1.CompositeResourceDefinition:
									d := v1beta1.CompositeResourceDefinition{}
									d.SetDeletionTimestamp(&now)
									*v = d
								case *extv1.CustomResourceDefinition:
									return errBoom
								}
								return nil
							}),
							MockStatusUpdate: test.NewMockStatusUpdateFn(nil),
						},
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"RemoveFinalizerError": {
			reason: "We should requeue after a short wait if we encounter an error while removing a finalizer.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil, func(o runtime.Object) error {
								if v, ok := o.(*v1beta1.CompositeResourceDefinition); ok {
									d := v1beta1.CompositeResourceDefinition{}
									d.SetDeletionTimestamp(&now)
									*v = d
								}
								return nil
							}),
							MockStatusUpdate: test.NewMockStatusUpdateFn(nil),
						},
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
					WithFinalizer(resource.FinalizerFns{RemoveFinalizerFn: func(_ context.Context, _ resource.Object) error {
						return errBoom
					}}),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"SuccessfulDelete": {
			reason: "We should not requeue when deleted if we successfully cleaned up our CRD and removed our finalizer.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil, func(o runtime.Object) error {
								if v, ok := o.(*v1beta1.CompositeResourceDefinition); ok {
									d := v1beta1.CompositeResourceDefinition{}
									d.SetDeletionTimestamp(&now)
									*v = d
								}
								return nil
							}),
							MockStatusUpdate: test.NewMockStatusUpdateFn(nil),
						},
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
					WithFinalizer(resource.FinalizerFns{RemoveFinalizerFn: func(_ context.Context, _ resource.Object) error {
						return nil
					}}),
				},
			},
			want: want{
				r: reconcile.Result{Requeue: false},
			},
		},
		"ListCustomResourcesError": {
			reason: "We should requeue after a short wait if we encounter an error while listing all defined resources.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil, func(o runtime.Object) error {
								switch v := o.(type) {
								case *v1beta1.CompositeResourceDefinition:
									d := v1beta1.CompositeResourceDefinition{}
									d.SetUID(owner)
									d.SetDeletionTimestamp(&now)
									*v = d
								case *extv1.CustomResourceDefinition:
									crd := extv1.CustomResourceDefinition{}
									crd.SetCreationTimestamp(now)
									crd.SetOwnerReferences([]metav1.OwnerReference{{UID: owner, Controller: &ctrlr}})
									*v = crd
								}
								return nil
							}),
							MockList:         test.NewMockListFn(errBoom),
							MockStatusUpdate: test.NewMockStatusUpdateFn(nil),
						},
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"DeleteCustomResourcesError": {
			reason: "We should requeue after a short wait if we encounter an error while deleting defined resources.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil, func(o runtime.Object) error {
								switch v := o.(type) {
								case *v1beta1.CompositeResourceDefinition:
									d := v1beta1.CompositeResourceDefinition{}
									d.SetUID(owner)
									d.SetDeletionTimestamp(&now)
									*v = d
								case *extv1.CustomResourceDefinition:
									crd := extv1.CustomResourceDefinition{}
									crd.SetCreationTimestamp(now)
									crd.SetOwnerReferences([]metav1.OwnerReference{{UID: owner, Controller: &ctrlr}})
									*v = crd
								}
								return nil
							}),
							MockList: test.NewMockListFn(nil, func(o runtime.Object) error {
								v := o.(*unstructured.UnstructuredList)
								*v = unstructured.UnstructuredList{
									Items: []unstructured.Unstructured{{}, {}},
								}
								return nil
							}),
							MockDelete:       test.NewMockDeleteFn(errBoom),
							MockStatusUpdate: test.NewMockStatusUpdateFn(nil),
						},
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"SuccessfulDeleteCustomResources": {
			reason: "We should requeue after a tiny wait to ensure our defined resources are gone before we remove our CRD.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil, func(o runtime.Object) error {
								switch v := o.(type) {
								case *v1beta1.CompositeResourceDefinition:
									d := v1beta1.CompositeResourceDefinition{}
									d.SetUID(owner)
									d.SetDeletionTimestamp(&now)
									*v = d
								case *extv1.CustomResourceDefinition:
									crd := extv1.CustomResourceDefinition{}
									crd.SetCreationTimestamp(now)
									crd.SetOwnerReferences([]metav1.OwnerReference{{UID: owner, Controller: &ctrlr}})
									*v = crd
								}
								return nil
							}),
							MockList: test.NewMockListFn(nil, func(o runtime.Object) error {
								v := o.(*unstructured.UnstructuredList)
								*v = unstructured.UnstructuredList{
									Items: []unstructured.Unstructured{{}, {}},
								}
								return nil
							}),
							MockDelete:       test.NewMockDeleteFn(nil),
							MockStatusUpdate: test.NewMockStatusUpdateFn(nil),
						},
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: tinyWait},
			},
		},
		"DeleteCustomResourceDefinitionError": {
			reason: "We should requeue after a short wait if we encounter an error while deleting the CRD we created.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil, func(o runtime.Object) error {
								switch v := o.(type) {
								case *v1beta1.CompositeResourceDefinition:
									d := v1beta1.CompositeResourceDefinition{
										Spec: v1beta1.CompositeResourceDefinitionSpec{},
									}
									d.SetUID(owner)
									d.SetDeletionTimestamp(&now)
									*v = d
								case *extv1.CustomResourceDefinition:
									crd := extv1.CustomResourceDefinition{}
									crd.SetCreationTimestamp(now)
									crd.SetOwnerReferences([]metav1.OwnerReference{{UID: owner, Controller: &ctrlr}})
									*v = crd
								}
								return nil
							}),
							MockList:         test.NewMockListFn(nil),
							MockDelete:       test.NewMockDeleteFn(errBoom),
							MockStatusUpdate: test.NewMockStatusUpdateFn(nil),
						},
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"SuccessfulCleanup": {
			reason: "We should requeue after a tiny wait to remove our finalizer once we've cleaned up our defined resources and CRD.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil, func(o runtime.Object) error {
								switch v := o.(type) {
								case *v1beta1.CompositeResourceDefinition:
									d := v1beta1.CompositeResourceDefinition{}
									d.SetUID(owner)
									d.SetDeletionTimestamp(&now)
									*v = d
								case *extv1.CustomResourceDefinition:
									crd := extv1.CustomResourceDefinition{}
									crd.SetCreationTimestamp(now)
									crd.SetOwnerReferences([]metav1.OwnerReference{{UID: owner, Controller: &ctrlr}})
									*v = crd
								}
								return nil
							}),
							MockList:   test.NewMockListFn(nil),
							MockDelete: test.NewMockDeleteFn(nil),
							MockStatusUpdate: test.NewMockStatusUpdateFn(nil, func(got runtime.Object) error {
								want := &v1beta1.CompositeResourceDefinition{}
								want.SetUID(owner)
								want.SetDeletionTimestamp(&now)
								want.Status.SetConditions(v1beta1.TerminatingClaim())

								if diff := cmp.Diff(want, got); diff != "" {
									t.Errorf("MockStatusUpdate: -want, +got:\n%s\n", diff)
								}

								return nil
							}),
						},
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: tinyWait},
			},
		},
		"AddFinalizerError": {
			reason: "We should requeue after a short wait if we encounter an error while adding a finalizer.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil),
						},
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
					WithFinalizer(resource.FinalizerFns{AddFinalizerFn: func(_ context.Context, _ resource.Object) error {
						return errBoom
					}}),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"ApplyCRDError": {
			reason: "We should requeue after a short wait if we encounter an error while applying our CRD.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil),
						},
						Applicator: resource.ApplyFn(func(_ context.Context, _ runtime.Object, _ ...resource.ApplyOption) error {
							return errBoom
						}),
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
					WithFinalizer(resource.FinalizerFns{AddFinalizerFn: func(_ context.Context, _ resource.Object) error {
						return nil
					}}),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"CustomResourceDefinitionIsNotEstablished": {
			reason: "We should requeue after a tiny wait if we're waiting for a newly created CRD to become established.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil),
						},
						Applicator: resource.ApplyFn(func(_ context.Context, _ runtime.Object, _ ...resource.ApplyOption) error {
							return nil
						}),
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{}, nil
					})),
					WithFinalizer(resource.FinalizerFns{AddFinalizerFn: func(_ context.Context, _ resource.Object) error {
						return nil
					}}),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: tinyWait},
			},
		},
		"StartControllerError": {
			reason: "We should requeue after a short wait if we encounter an error while starting our controller.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil),
						},
						Applicator: resource.ApplyFn(func(_ context.Context, _ runtime.Object, _ ...resource.ApplyOption) error {
							return nil
						}),
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{
							Status: extv1.CustomResourceDefinitionStatus{
								Conditions: []extv1.CustomResourceDefinitionCondition{
									{Type: extv1.Established, Status: extv1.ConditionTrue},
								},
							},
						}, nil
					})),
					WithFinalizer(resource.FinalizerFns{AddFinalizerFn: func(_ context.Context, _ resource.Object) error {
						return nil
					}}),
					WithControllerEngine(&MockEngine{
						MockErr:   func(_ string) error { return nil },
						MockStart: func(_ string, _ kcontroller.Options, _ ...controller.Watch) error { return errBoom },
					}),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"SuccessfulStart": {
			reason: "We should not requeue after a short wait if we successfully ensured our CRD exists and controller is started.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil),
							MockStatusUpdate: test.NewMockStatusUpdateFn(nil, func(o runtime.Object) error {
								want := &v1beta1.CompositeResourceDefinition{}
								want.Status.SetConditions(v1beta1.WatchingClaim())

								if diff := cmp.Diff(want, o); diff != "" {
									t.Errorf("-want, +got:\n%s", diff)
								}
								return nil
							}),
						},
						Applicator: resource.ApplyFn(func(_ context.Context, _ runtime.Object, _ ...resource.ApplyOption) error {
							return nil
						}),
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{
							Status: extv1.CustomResourceDefinitionStatus{
								Conditions: []extv1.CustomResourceDefinitionCondition{
									{Type: extv1.Established, Status: extv1.ConditionTrue},
								},
							},
						}, nil
					})),
					WithFinalizer(resource.FinalizerFns{AddFinalizerFn: func(_ context.Context, _ resource.Object) error {
						return nil
					}}),
					WithControllerEngine(&MockEngine{
						MockErr:   func(name string) error { return errBoom }, // This error should only be logged.
						MockStart: func(_ string, _ kcontroller.Options, _ ...controller.Watch) error { return nil }},
					),
				},
			},
			want: want{
				r: reconcile.Result{Requeue: false},
			},
		},
		"SuccessfulUpdateControllerVersion": {
			reason: "We should not requeue after a short wait if we successfully ensured our CRD exists, the old controller stopped, and the new one started.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil, func(obj runtime.Object) error {
								d := obj.(*v1beta1.CompositeResourceDefinition)
								d.Spec.ClaimNames = &extv1.CustomResourceDefinitionNames{}
								d.Spec.Versions = []v1beta1.CompositeResourceDefinitionVersion{
									{Name: "old", Referenceable: false},
									{Name: "new", Referenceable: true},
								}
								d.Status.Controllers.CompositeResourceClaimTypeRef = v1beta1.TypeReference{APIVersion: "old"}
								return nil
							}),
							MockStatusUpdate: test.NewMockStatusUpdateFn(nil, func(o runtime.Object) error {
								want := &v1beta1.CompositeResourceDefinition{}
								want.Spec.ClaimNames = &extv1.CustomResourceDefinitionNames{}
								want.Spec.Versions = []v1beta1.CompositeResourceDefinitionVersion{
									{Name: "old", Referenceable: false},
									{Name: "new", Referenceable: true},
								}
								want.Status.Controllers.CompositeResourceClaimTypeRef = v1beta1.TypeReference{APIVersion: "new"}
								want.Status.SetConditions(v1beta1.WatchingClaim())

								if diff := cmp.Diff(want, o); diff != "" {
									t.Errorf("-want, +got:\n%s", diff)
								}
								return nil
							}),
						},
						Applicator: resource.ApplyFn(func(_ context.Context, _ runtime.Object, _ ...resource.ApplyOption) error {
							return nil
						}),
					}),
					WithCRDRenderer(CRDRenderFn(func(_ *v1beta1.CompositeResourceDefinition) (*extv1.CustomResourceDefinition, error) {
						return &extv1.CustomResourceDefinition{
							Status: extv1.CustomResourceDefinitionStatus{
								Conditions: []extv1.CustomResourceDefinitionCondition{
									{Type: extv1.Established, Status: extv1.ConditionTrue},
								},
							},
						}, nil
					})),
					WithFinalizer(resource.FinalizerFns{AddFinalizerFn: func(_ context.Context, _ resource.Object) error {
						return nil
					}}),
					WithControllerEngine(&MockEngine{
						MockErr:   func(name string) error { return nil },
						MockStart: func(_ string, _ kcontroller.Options, _ ...controller.Watch) error { return nil },
						MockStop:  func(_ string) {},
					}),
				},
			},
			want: want{
				r: reconcile.Result{Requeue: false},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := NewReconciler(tc.args.mgr, tc.args.opts...)
			got, err := r.Reconcile(reconcile.Request{})

			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nr.Reconcile(...): -want error, +got error:\n%s", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.r, got, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nr.Reconcile(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}
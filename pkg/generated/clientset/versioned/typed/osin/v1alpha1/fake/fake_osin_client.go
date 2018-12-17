// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v1alpha1 "github.com/openshift/cluster-osin-operator/pkg/generated/clientset/versioned/typed/osin/v1alpha1"
	rest "k8s.io/client-go/rest"
	testing "k8s.io/client-go/testing"
)

type FakeOsinV1alpha1 struct {
	*testing.Fake
}

func (c *FakeOsinV1alpha1) Osins(namespace string) v1alpha1.OsinInterface {
	return &FakeOsins{c, namespace}
}

// RESTClient returns a RESTClient that is used to communicate
// with API server by this client implementation.
func (c *FakeOsinV1alpha1) RESTClient() rest.Interface {
	var ret *rest.RESTClient
	return ret
}

//
// DISCLAIMER
//
// Copyright 2018 ArangoDB GmbH, Cologne, Germany
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Copyright holder is ArangoDB GmbH, Cologne, Germany
//

// This file was automatically generated by lister-gen

package v1alpha

import (
	v1alpha "github.com/arangodb/k8s-operator/pkg/apis/deployment/v1alpha"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// ArangoDeploymentLister helps list ArangoDeployments.
type ArangoDeploymentLister interface {
	// List lists all ArangoDeployments in the indexer.
	List(selector labels.Selector) (ret []*v1alpha.ArangoDeployment, err error)
	// ArangoDeployments returns an object that can list and get ArangoDeployments.
	ArangoDeployments(namespace string) ArangoDeploymentNamespaceLister
	ArangoDeploymentListerExpansion
}

// arangoDeploymentLister implements the ArangoDeploymentLister interface.
type arangoDeploymentLister struct {
	indexer cache.Indexer
}

// NewArangoDeploymentLister returns a new ArangoDeploymentLister.
func NewArangoDeploymentLister(indexer cache.Indexer) ArangoDeploymentLister {
	return &arangoDeploymentLister{indexer: indexer}
}

// List lists all ArangoDeployments in the indexer.
func (s *arangoDeploymentLister) List(selector labels.Selector) (ret []*v1alpha.ArangoDeployment, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha.ArangoDeployment))
	})
	return ret, err
}

// ArangoDeployments returns an object that can list and get ArangoDeployments.
func (s *arangoDeploymentLister) ArangoDeployments(namespace string) ArangoDeploymentNamespaceLister {
	return arangoDeploymentNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// ArangoDeploymentNamespaceLister helps list and get ArangoDeployments.
type ArangoDeploymentNamespaceLister interface {
	// List lists all ArangoDeployments in the indexer for a given namespace.
	List(selector labels.Selector) (ret []*v1alpha.ArangoDeployment, err error)
	// Get retrieves the ArangoDeployment from the indexer for a given namespace and name.
	Get(name string) (*v1alpha.ArangoDeployment, error)
	ArangoDeploymentNamespaceListerExpansion
}

// arangoDeploymentNamespaceLister implements the ArangoDeploymentNamespaceLister
// interface.
type arangoDeploymentNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all ArangoDeployments in the indexer for a given namespace.
func (s arangoDeploymentNamespaceLister) List(selector labels.Selector) (ret []*v1alpha.ArangoDeployment, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha.ArangoDeployment))
	})
	return ret, err
}

// Get retrieves the ArangoDeployment from the indexer for a given namespace and name.
func (s arangoDeploymentNamespaceLister) Get(name string) (*v1alpha.ArangoDeployment, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1alpha.Resource("arangodeployment"), name)
	}
	return obj.(*v1alpha.ArangoDeployment), nil
}

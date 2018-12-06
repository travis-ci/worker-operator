package controller

import (
	"github.com/travis-ci/worker-operator/pkg/controller/workercluster"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, workercluster.Add)
}

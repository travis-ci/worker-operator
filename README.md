# worker-operator

A Kubernetes [operator][] for running a cluster of Travis CI [worker][] processes.

[operator]: https://coreos.com/operators/
[worker]: https://github.com/travis-ci/worker

## Why?

None of the built-in strategies in Kubernete's Deployments are sufficient alone to roll out a new version of `worker` correctly.

`worker` needs to be careful to not run too many concurrent jobs, since those jobs depend on external computing resources that are not infinite. Gracefully shutting down an instance of `worker` requires waiting for the currently running jobs to finish, which can take on the order of hours rather than seconds. While we can handle a forceful termination by restarting jobs, we don't want to make it a regular part of our rollouts.

During a graceful shutdown, it's important that the new `worker` processes brought up to replace those do not try to run too many new jobs. Normally, the pool size of a `worker` is fixed and configured in the environment of the pod. In this model, we need to manage the number of new `worker` instances that are brought up so that the rollout is gradual and keeps the overall number of concurrent jobs in an acceptable range.

Kubernetes doesn't actually support this, though. It will only wait for a pod to be marked for deletion (and sent its stop signal), but not for the pod to actually finish its work. This is probably fine when your pods don't use a finite external resource and can normally shut down in a few seconds, but in our case, we **really** need to wait for this shutdown to happen.

## What does `worker-operator` do?

It provides a new `WorkerCluster` custom resource. Each WorkerCluster creates an ordinary Deployment, so that we can reuse as much normal Kubernetes behavior as possible.

A WorkerCluster does not specify a number of replicas or how many jobs each replica should run, like we would have to when running `worker` just in a Deployment. Instead, when you create a WorkerCluster, you specify **the maximum number of jobs that should run concurrently.** The operator does the math to determine how many replicas should be running based on that. If you reconfigure the cluster to run a different number of jobs, it adjusts the deployment appropriately if needed.

But that's just a little math. It doesn't really justify a whole operator. What's interesting is what happens when we rollout a new version or configuration of `worker` using a `WorkerCluster`.

The main goal of `worker-operator` is to **dynamically adjust pool sizes of workers.** When rolling out new worker pods, the operator will take an inventory of all the pods running in the WorkerCluster, noting their current pool size and whether they are running normally or in the process of shutting down. The operator will scale up the pools in the new processes as the old processes naturally drain their pools. 

This means we can use the normal behavior of Kubernetes to roll out new pods immediately and just scale them up incrementally rather than all at once. All of this happens without any manual intervention.

## Examples

TODO. The actual specification of the custom WorkerCluster resource is still in flux.

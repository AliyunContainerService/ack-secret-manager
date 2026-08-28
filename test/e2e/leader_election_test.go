// leader_election_test.go - E2E tests for the controller-runtime Lease-based
// leader election introduced in v0.6.7 (LeaderElectionID
// "ack-secret-manager-lock", POD_NAMESPACE injection, ReleaseOnCancel).
//
// Contract under test:
//  1. Exclusivity: the Lease holderIdentity always corresponds to one of the
//     currently Running controller Pods and the renewTime stays fresh, i.e.
//     exactly one live replica owns leadership.
//  2. Handover: after a rolling restart the old holder is released
//     (ReleaseOnCancel) and a new leader Pod acquires the Lease with a fresh
//     renewTime.
//  3. Uniqueness under multi-replica: with replicas=2 exactly one of the two
//     Running Pods holds the Lease and keeps renewing it.
//  4. Crash takeover: when the leader is force-killed (no graceful release),
//     a new Pod acquires the Lease after the leaseDuration timeout.
package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Leader Election E2E", func() {
	ctx := context.Background()

	It("Should hold the leader election Lease exclusively by a running controller Pod", func() {
		// Full contract check (existence, non-empty holderIdentity matching a
		// Running controller Pod, fresh renewTime within the 30s window).
		verifyLeaderElectionLease(ctx)
	})

	It("Should hand over leadership to a new leader Pod after a rolling restart", func() {
		leaseNamespace := leaderElectionLeaseNamespace(ctx)

		By("capturing the current holderIdentity before the restart")
		lease, err := clientset.CoordinationV1().Leases(leaseNamespace).Get(ctx, leaderElectionLeaseName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Lease %s/%s should exist before the handover spec", leaseNamespace, leaderElectionLeaseName)
		Expect(lease.Spec.HolderIdentity).NotTo(BeNil(), "Lease holderIdentity must be set before the handover spec")
		previousHolder := *lease.Spec.HolderIdentity
		Expect(previousHolder).NotTo(BeEmpty(), "Lease holderIdentity must not be empty before the handover spec")

		By("forcing a rolling restart of the controller Deployment")
		// The empty mutator keeps the spec untouched: only the restartedAt
		// annotation (set by the helper) drives the rollout. The helper also
		// re-confirms leadership once the rollout completes.
		updateDeploymentAndRollout(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName, func(dep *appsv1.Deployment) {})

		By("observing the Lease hand over to a new leader Pod with a fresh renewTime")
		Eventually(func(g Gomega) {
			lease, err := clientset.CoordinationV1().Leases(leaseNamespace).Get(ctx, leaderElectionLeaseName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), "Lease %s/%s should keep existing across the handover", leaseNamespace, leaderElectionLeaseName)
			g.Expect(lease.Spec.HolderIdentity).NotTo(BeNil(), "Lease holderIdentity must be set after the handover")
			holder := *lease.Spec.HolderIdentity
			g.Expect(holder).NotTo(BeEmpty(), "Lease holderIdentity must not be empty after the handover")
			g.Expect(holder).NotTo(Equal(previousHolder),
				"holderIdentity should change after the rolling restart (ReleaseOnCancel + new leader)")
			g.Expect(lease.Spec.RenewTime).NotTo(BeNil(), "Lease renewTime must be set after the handover")
			g.Expect(time.Since(lease.Spec.RenewTime.Time)).To(BeNumerically("<", leaderRenewFreshnessWindow),
				"the new leader should actively renew the Lease within the freshness window")

			podNames, err := runningControllerPodNames(ctx, ackSecretManagerNamespace)
			g.Expect(err).NotTo(HaveOccurred(), "controller Pods should be listable for the holderIdentity consistency check")
			g.Expect(leaseHolderMatchesRunningPod(holder, podNames)).To(BeTrue(),
				"the new holderIdentity %q must correspond to one of the Running controller Pods %v", holder, podNames)
		}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
			"leader election Lease %s/%s was not handed over to a new running Pod after the rolling restart",
			leaseNamespace, leaderElectionLeaseName)
	})

	It("Should keep exactly one leader when the Deployment is scaled to two replicas", func() {
		By("capturing the original replica count and registering baseline restoration")
		dep, err := clientset.AppsV1().Deployments(ackSecretManagerNamespace).Get(ctx, ackSecretManagerDeploymentName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to get Deployment %s/%s for the replica baseline", ackSecretManagerNamespace, ackSecretManagerDeploymentName)
		Expect(dep.Spec.Replicas).NotTo(BeNil(), "Deployment %s/%s must have an explicit replica count", ackSecretManagerNamespace, ackSecretManagerDeploymentName)
		originalReplicas := *dep.Spec.Replicas

		// Registered before the scale so the baseline is restored even on
		// failure paths (DeferCleanup runs LIFO).
		DeferCleanup(func() {
			restoreDeploymentReplicas(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName, originalReplicas)
		})

		By("scaling the controller Deployment to 2 replicas")
		dep.Spec.Replicas = ptrToInt32(2)
		_, err = clientset.AppsV1().Deployments(ackSecretManagerNamespace).Update(ctx, dep, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to scale Deployment %s/%s to 2 replicas", ackSecretManagerNamespace, ackSecretManagerDeploymentName)
		waitForDeploymentRollout(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)

		By("asserting exactly one of the two Running Pods holds the Lease and keeps renewing it")
		// Argumentation: controller-runtime leader election uses a single
		// coordination.k8s.io Lease object as the lock, so by construction at
		// most one holderIdentity can be recorded on it. Asserting that the
		// holder is one of the two Running replicas while the renewTime stays
		// continuously fresh therefore proves that exactly one Pod holds the
		// lock and the other (standby) replica does not.
		//
		// Full contract check first (non-empty holderIdentity corresponding to
		// a Running controller Pod plus a fresh renewTime within the window).
		verifyLeaderElectionLease(ctx)

		// Population-aware uniqueness check: the holder must be one of the two
		// Running replicas and keep renewing the Lease over the whole window.
		// Consistently (not Eventually) is required here: every sample across
		// the observation window must satisfy the invariant, so an alternating
		// split-brain where both replicas take turns writing the Lease cannot
		// pass by being sampled at a lucky single instant.
		leaseNamespace := leaderElectionLeaseNamespace(ctx)
		Consistently(func(g Gomega) {
			podNames, err := runningControllerPodNames(ctx, ackSecretManagerNamespace)
			g.Expect(err).NotTo(HaveOccurred(), "controller Pods should be listable for the two-replica holder check")
			g.Expect(podNames).To(HaveLen(2),
				"both scaled-up controller replicas should be Running before asserting holder uniqueness")

			lease, err := clientset.CoordinationV1().Leases(leaseNamespace).Get(ctx, leaderElectionLeaseName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), "Lease %s/%s should exist for the two-replica holder check", leaseNamespace, leaderElectionLeaseName)
			g.Expect(lease.Spec.HolderIdentity).NotTo(BeNil(), "Lease holderIdentity must be set with two replicas")
			holder := *lease.Spec.HolderIdentity
			g.Expect(holder).NotTo(BeEmpty(), "Lease holderIdentity must not be empty with two replicas")
			g.Expect(leaseHolderPodName(holder)).To(BeElementOf(podNames),
				"the single Lease holder %q must be one of the two Running replicas %v", holder, podNames)
			g.Expect(lease.Spec.RenewTime).NotTo(BeNil(), "Lease renewTime must be set with two replicas")
			g.Expect(time.Since(lease.Spec.RenewTime.Time)).To(BeNumerically("<", leaderRenewFreshnessWindow),
				"the sole holder should keep renewing the Lease while a standby replica exists")
		}).WithTimeout(60*time.Second).WithPolling(5*time.Second).Should(Succeed(),
			"with replicas=2 the leader election Lease %s/%s was not held exclusively by one of the two Running Pods over the whole observation window",
			leaseNamespace, leaderElectionLeaseName)
	})

	It("Should hand over leadership via Lease expiry after the leader Pod is force-killed", func() {
		// This spec exercises the crash path: the leader is deleted with
		// gracePeriodSeconds=0, so ReleaseOnCancel never runs and the Lease is
		// NOT gracefully released. A standby/new Pod may only take over after
		// the stale leaseDuration (15s) expires, which is the automatic
		// takeover behaviour this spec verifies — in contrast to the graceful
		// rolling-restart handover covered by the spec above.
		leaseNamespace := leaderElectionLeaseNamespace(ctx)

		// We must guarantee that no second controller Pod is around right before
		// the leader gets force-killed, otherwise the test could not
		// legitimately be the one that wins the crash race — instead we would
		// only be racing against the already-running stand-by replica. Scale to
		// 1 first. Capture the current replica count first and register a
		// deferred restoration, because an early abort (e.g. this spec failing)
		// must never leak the scaled-down Deployment into later specs.
		By("scaling the Deployment down to 1 replica so no standby Pod can take over the Lease")
		dep, err := clientset.AppsV1().Deployments(ackSecretManagerNamespace).Get(ctx, ackSecretManagerDeploymentName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to get Deployment for scale-down")
		Expect(dep.Spec.Replicas).NotTo(BeNil(), "Deployment %s/%s must have an explicit replica count", ackSecretManagerNamespace, ackSecretManagerDeploymentName)
		originalReplicas := *dep.Spec.Replicas

		// Registered before the scale so the baseline is restored even on
		// failure paths (DeferCleanup runs LIFO).
		DeferCleanup(func() {
			restoreDeploymentReplicas(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName, originalReplicas)
		})

		dep.Spec.Replicas = ptrToInt32(1)
		_, err = clientset.AppsV1().Deployments(ackSecretManagerNamespace).Update(ctx, dep, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to scale Deployment to 1 replica")
		waitForDeploymentRollout(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)

		// After the scale-down, the leader election state may still be
		// transitioning (the terminated Pod's client may have released the
		// Lease, and the surviving Pod may be acquiring it). Wait until the
		// Lease is stable: a running Pod actively renews it.
		By("waiting for the leader election Lease to stabilise after scale-down")
		verifyLeaderElectionLease(ctx)

		By("capturing the current holderIdentity and confirming it is a Running Pod")
		lease, err := clientset.CoordinationV1().Leases(leaseNamespace).Get(ctx, leaderElectionLeaseName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Lease %s/%s should exist before the crash spec", leaseNamespace, leaderElectionLeaseName)
		Expect(lease.Spec.HolderIdentity).NotTo(BeNil(), "Lease holderIdentity must be set before the crash spec")
		previousHolder := *lease.Spec.HolderIdentity
		Expect(previousHolder).NotTo(BeEmpty(), "Lease holderIdentity must not be empty before the crash spec")
		leaderPodName := leaseHolderPodName(previousHolder)

		podNames, err := runningControllerPodNames(ctx, ackSecretManagerNamespace)
		Expect(err).NotTo(HaveOccurred(), "controller Pods should be listable before force-killing the leader")
		Expect(podNames).To(ContainElement(leaderPodName),
			"the current leader Pod %q must be Running before it is force-killed", leaderPodName)

		// Registered before the force-kill so a stable leadership state is
		// re-established even when the takeover assertions fail (DeferCleanup
		// runs on failure paths too): the spec is the only leader-state
		// mutation without a structural restore (unlike the replicas spec), so
		// the leaderless/unverified state must not leak into subsequent specs.
		DeferCleanup(func() {
			verifyLeaderElectionLease(ctx)
		})

		By(fmt.Sprintf("force-killing the leader Pod %s (gracePeriodSeconds=0, no graceful lease release)", leaderPodName))
		// GracePeriodSeconds=0 deletes the Pod immediately, simulating a crash:
		// the process gets no shutdown window to release the Lease.
		gracePeriod := int64(0)
		err = clientset.CoreV1().Pods(ackSecretManagerNamespace).Delete(ctx, leaderPodName, metav1.DeleteOptions{
			GracePeriodSeconds: &gracePeriod,
		})
		Expect(err).NotTo(HaveOccurred(), "failed to force-kill the leader Pod %s", leaderPodName)

		By("waiting for a new leader to take over the Lease after the crash")
		// Takeover requires: leaseDuration (15s) expiry on the stale lease +
		// the Deployment controller spinning up a replacement Pod + that Pod
		// acquiring and renewing the Lease; 3 minutes covers the whole chain.
		Eventually(func(g Gomega) {
			lease, err := clientset.CoordinationV1().Leases(leaseNamespace).Get(ctx, leaderElectionLeaseName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred(), "Lease %s/%s should keep existing across the crash takeover", leaseNamespace, leaderElectionLeaseName)
			g.Expect(lease.Spec.HolderIdentity).NotTo(BeNil(), "Lease holderIdentity must be set after the crash takeover")
			holder := *lease.Spec.HolderIdentity
			g.Expect(holder).NotTo(BeEmpty(), "Lease holderIdentity must not be empty after the crash takeover")
			g.Expect(holder).NotTo(Equal(previousHolder),
				"holderIdentity should change after the force-killed leader (new leader acquires the expired Lease)")
			g.Expect(lease.Spec.RenewTime).NotTo(BeNil(), "Lease renewTime must be set after the crash takeover")
			g.Expect(time.Since(lease.Spec.RenewTime.Time)).To(BeNumerically("<", leaderRenewFreshnessWindow),
				"the new leader should actively renew the Lease within the freshness window")

			podNames, err := runningControllerPodNames(ctx, ackSecretManagerNamespace)
			g.Expect(err).NotTo(HaveOccurred(), "controller Pods should be listable for the holderIdentity consistency check")
			g.Expect(leaseHolderMatchesRunningPod(holder, podNames)).To(BeTrue(),
				"the new holderIdentity %q must correspond to one of the Running controller Pods %v", holder, podNames)
		}).WithTimeout(3*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
			"leader election Lease %s/%s was not taken over by a new running Pod after force-killing the leader",
			leaseNamespace, leaderElectionLeaseName)

		By("re-confirming a stable leadership state after the crash takeover")
		verifyLeaderElectionLease(ctx)
	})
})

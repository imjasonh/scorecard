// Copyright 2020 Security Scorecard Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package checks

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/ossf/scorecard/v3/checker"
	"github.com/ossf/scorecard/v3/clients"
	sce "github.com/ossf/scorecard/v3/errors"
)

const (
	// CheckBranchProtection is the exported name for Branch-Protected check.
	CheckBranchProtection = "Branch-Protection"
	minReviews            = 2
	// Points incremented at each level.
	adminNonAdminBasicLevel     = 3 // Level 1.
	adminNonAdminReviewLevel    = 3 // Level 2.
	nonAdminContextLevel        = 2 // Level 3.
	nonAdminThoroughReviewLevel = 1 // Level 4.
	adminThoroughReviewLevel    = 1 // Level 5.
)

type scoresInfo struct {
	basic               int
	adminBasic          int
	review              int
	adminReview         int
	context             int
	thoroughReview      int
	adminThoroughReview int
}

// Maximum score depending on whether admin token is used.
type levelScore struct {
	scores scoresInfo // Score result for a branch.
	maxes  scoresInfo // Maximum possible score for a branch.
}

//nolint:gochecknoinits
func init() {
	registerCheck(CheckBranchProtection, BranchProtection)
}

type branchMap map[string]*clients.BranchRef

func (b branchMap) getBranchByName(name string) (*clients.BranchRef, error) {
	val, exists := b[name]
	if exists {
		return val, nil
	}

	// Ideally, we should check using repositories.GetBranch if there was a branch redirect.
	// See https://github.com/google/go-github/issues/1895
	// For now, handle the common master -> main redirect.
	if name == "master" {
		val, exists := b["main"]
		if exists {
			return val, nil
		}
	}
	return nil, sce.WithMessage(sce.ErrScorecardInternal,
		fmt.Sprintf("could not find branch name %s: %v", name, errInternalBranchNotFound))
}

func getBranchMapFrom(branches []*clients.BranchRef) branchMap {
	ret := make(branchMap)
	for _, branch := range branches {
		branchName := getBranchName(branch)
		if branchName != "" {
			ret[branchName] = branch
		}
	}
	return ret
}

func getBranchName(branch *clients.BranchRef) string {
	if branch == nil || branch.Name == nil {
		return ""
	}
	return *branch.Name
}

// BranchProtection runs Branch-Protection check.
func BranchProtection(c *checker.CheckRequest) checker.CheckResult {
	// Checks branch protection on both release and development branch.
	return checkReleaseAndDevBranchProtection(c.RepoClient, c.Dlogger)
}

func computeNonAdminBasicScore(scores []levelScore) int {
	score := 0
	for _, s := range scores {
		score += s.scores.basic
	}
	return score
}

func computeAdminBasicScore(scores []levelScore) int {
	score := 0
	for _, s := range scores {
		score += s.scores.adminBasic
	}
	return score
}

func computeNonAdminReviewScore(scores []levelScore) int {
	score := 0
	for _, s := range scores {
		score += s.scores.review
	}
	return score
}

func computeAdminReviewScore(scores []levelScore) int {
	score := 0
	for _, s := range scores {
		score += s.scores.adminReview
	}
	return score
}

func computeNonAdminThoroughReviewScore(scores []levelScore) int {
	score := 0
	for _, s := range scores {
		score += s.scores.thoroughReview
	}
	return score
}

func computeAdminThoroughReviewScore(scores []levelScore) int {
	score := 0
	for _, s := range scores {
		score += s.scores.adminThoroughReview
	}
	return score
}

func computeNonAdminContextScore(scores []levelScore) int {
	score := 0
	for _, s := range scores {
		score += s.scores.context
	}
	return score
}

func noarmalizeScore(score, max, level int) float64 {
	if max == 0 {
		return float64(level)
	}
	return float64(score*level) / float64(max)
}

func computeScore(scores []levelScore) (int, error) {
	if len(scores) == 0 {
		return 0, sce.WithMessage(sce.ErrScorecardInternal, "scores are empty")
	}

	score := float64(0)
	maxScore := scores[0].maxes

	// First, check if they all pass the basic (admin and non-admin) checks.
	maxBasicScore := maxScore.basic * len(scores)
	maxAdminBasicScore := maxScore.adminBasic * len(scores)
	basicScore := computeNonAdminBasicScore(scores)
	adminBasicScore := computeAdminBasicScore(scores)
	score += noarmalizeScore(basicScore+adminBasicScore, maxBasicScore+maxAdminBasicScore, adminNonAdminBasicLevel)
	if basicScore != maxBasicScore ||
		adminBasicScore != maxAdminBasicScore {
		return int(score), nil
	}

	// Second, check the (admin and non-admin) reviews.
	maxReviewScore := maxScore.review * len(scores)
	maxAdminReviewScore := maxScore.adminReview * len(scores)
	reviewScore := computeNonAdminReviewScore(scores)
	adminReviewScore := computeAdminReviewScore(scores)
	score += noarmalizeScore(reviewScore+adminReviewScore, maxReviewScore+maxAdminReviewScore, adminNonAdminReviewLevel)
	if reviewScore != maxReviewScore ||
		adminReviewScore != maxAdminReviewScore {
		return int(score), nil
	}

	// Third, check the use of non-admin context.
	maxContextScore := maxScore.context * len(scores)
	contextScore := computeNonAdminContextScore(scores)
	score += noarmalizeScore(contextScore, maxContextScore, nonAdminContextLevel)
	if contextScore != maxContextScore {
		return int(score), nil
	}

	// Fourth, check the thorough non-admin reviews.
	maxThoroughReviewScore := maxScore.thoroughReview * len(scores)
	thoroughReviewScore := computeNonAdminThoroughReviewScore(scores)
	score += noarmalizeScore(thoroughReviewScore, maxThoroughReviewScore, nonAdminThoroughReviewLevel)
	if thoroughReviewScore != maxThoroughReviewScore {
		return int(score), nil
	}

	// Last, check the thorough admin review config.
	// This one is controversial and has usability issues
	// https://github.com/ossf/scorecard/issues/1027, so we may remove it.
	maxAdminThoroughReviewScore := maxScore.adminThoroughReview * len(scores)
	adminThoroughReviewScore := computeAdminThoroughReviewScore(scores)
	score += noarmalizeScore(adminThoroughReviewScore, maxAdminThoroughReviewScore, adminThoroughReviewLevel)
	if adminThoroughReviewScore != maxAdminThoroughReviewScore {
		return int(score), nil
	}

	return int(score), nil
}

func info(dl checker.DetailLogger, doLogging bool, desc string, args ...interface{}) {
	if !doLogging {
		return
	}

	dl.Info(desc, args...)
}

func debug(dl checker.DetailLogger, doLogging bool, desc string, args ...interface{}) {
	if !doLogging {
		return
	}

	dl.Debug(desc, args...)
}

func warn(dl checker.DetailLogger, doLogging bool, desc string, args ...interface{}) {
	if !doLogging {
		return
	}

	dl.Warn(desc, args...)
}

func checkReleaseAndDevBranchProtection(
	repoClient clients.RepoClient, dl checker.DetailLogger) checker.CheckResult {
	// Get all branches. This will include information on whether they are protected.
	branches, err := repoClient.ListBranches()
	if err != nil {
		e := sce.WithMessage(sce.ErrScorecardInternal, err.Error())
		return checker.CreateRuntimeErrorResult(CheckBranchProtection, e)
	}
	branchesMap := getBranchMapFrom(branches)

	// Get release branches.
	releases, err := repoClient.ListReleases()
	if err != nil {
		e := sce.WithMessage(sce.ErrScorecardInternal, err.Error())
		return checker.CreateRuntimeErrorResult(CheckBranchProtection, e)
	}

	commit := regexp.MustCompile("^[a-f0-9]{40}$")
	checkBranches := make(map[string]bool)
	for _, release := range releases {
		if release.TargetCommitish == "" {
			// Log with a named error if target_commitish is nil.
			e := sce.WithMessage(sce.ErrScorecardInternal, errInternalCommitishNil.Error())
			return checker.CreateRuntimeErrorResult(CheckBranchProtection, e)
		}

		// TODO: if this is a sha, get the associated branch. for now, ignore.
		if commit.Match([]byte(release.TargetCommitish)) {
			continue
		}

		// Try to resolve the branch name.
		b, err := branchesMap.getBranchByName(release.TargetCommitish)
		if err != nil {
			// If the commitish branch is still not found, fail.
			return checker.CreateRuntimeErrorResult(CheckBranchProtection, err)
		}

		// Branch is valid, add to list of branches to check.
		checkBranches[*b.Name] = true
	}

	// Add default branch.
	defaultBranch, err := repoClient.GetDefaultBranch()
	if err != nil {
		return checker.CreateRuntimeErrorResult(CheckBranchProtection, err)
	}
	defaultBranchName := getBranchName(defaultBranch)
	if defaultBranchName != "" {
		checkBranches[defaultBranchName] = true
	}

	var scores []levelScore

	// Check protections on all the branches.
	for b := range checkBranches {
		var score levelScore
		branch, err := branchesMap.getBranchByName(b)
		if err != nil {
			if errors.Is(err, errInternalBranchNotFound) {
				continue
			}
			return checker.CreateRuntimeErrorResult(CheckBranchProtection, err)
		}
		// Protected field only indates that the branch matches
		// one `Branch protection rules`. All settings may be disabled,
		// so it does not provide any guarantees.
		protected := !(branch.Protected != nil && !*branch.Protected)
		if !protected {
			dl.Warn("branch protection not enabled for branch '%s'", b)
		}
		score.scores.basic, score.maxes.basic =
			basicNonAdminProtection(&branch.BranchProtectionRule, b, dl, protected)
		score.scores.adminBasic, score.maxes.adminBasic =
			basicAdminProtection(&branch.BranchProtectionRule, b, dl, protected)
		score.scores.review, score.maxes.review =
			nonAdminReviewProtection(&branch.BranchProtectionRule)
		score.scores.adminReview, score.maxes.adminReview =
			adminReviewProtection(&branch.BranchProtectionRule, b, dl, protected)
		score.scores.context, score.maxes.context =
			nonAdminContextProtection(&branch.BranchProtectionRule, b, dl, protected)
		score.scores.thoroughReview, score.maxes.thoroughReview =
			nonAdminThoroughReviewProtection(&branch.BranchProtectionRule, b, dl, protected)
		score.scores.adminThoroughReview, score.maxes.adminThoroughReview =
			adminThoroughReviewProtection(&branch.BranchProtectionRule, b, dl, protected) // Do we want this?

		scores = append(scores, score)
	}

	if len(scores) == 0 {
		return checker.CreateInconclusiveResult(CheckBranchProtection, "unable to detect any development/release branches")
	}

	score, err := computeScore(scores)
	if err != nil {
		return checker.CreateRuntimeErrorResult(CheckBranchProtection, err)
	}

	switch score {
	case checker.MinResultScore:
		return checker.CreateMinScoreResult(CheckBranchProtection,
			"branch protection not enabled on development/release branches")
	case checker.MaxResultScore:
		return checker.CreateMaxScoreResult(CheckBranchProtection,
			"branch protection is fully enabled on development and all release branches")
	default:
		return checker.CreateResultWithScore(CheckBranchProtection,
			"branch protection is not maximal on development and all release branches", score)
	}
}

func basicNonAdminProtection(protection *clients.BranchProtectionRule,
	branch string, dl checker.DetailLogger, doLogging bool) (int, int) {
	score := 0
	max := 0

	max++
	if protection.AllowForcePushes != nil {
		switch *protection.AllowForcePushes {
		case true:
			warn(dl, doLogging, "'force pushes' enabled on branch '%s'", branch)
		case false:
			info(dl, doLogging, "'force pushes' disabled on branch '%s'", branch)
			score++
		}
	}

	max++
	if protection.AllowDeletions != nil {
		switch *protection.AllowDeletions {
		case true:
			warn(dl, doLogging, "'allow deletion' enabled on branch '%s'", branch)
		case false:
			info(dl, doLogging, "'allow deletion' disabled on branch '%s'", branch)
			score++
		}
	}

	return score, max
}

func basicAdminProtection(protection *clients.BranchProtectionRule,
	branch string, dl checker.DetailLogger, doLogging bool) (int, int) {
	score := 0
	max := 0

	// nil typically means we do not have access to the value.
	if protection.EnforceAdmins != nil {
		// Note: we don't inrecase max possible score for non-admin viewers.
		max++
		switch *protection.EnforceAdmins {
		case true:
			info(dl, doLogging, "settings apply to administrators on branch '%s'", branch)
			score++
		case false:
			warn(dl, doLogging, "settings do not apply to administrators on branch '%s'", branch)
		}
	} else {
		debug(dl, doLogging, "unable to retrieve whether or not settings apply to administrators on branch '%s'", branch)
	}

	return score, max
}

func nonAdminContextProtection(protection *clients.BranchProtectionRule, branch string,
	dl checker.DetailLogger, doLogging bool) (int, int) {
	score := 0
	max := 0
	// This means there are specific checks enabled.
	// If only `Requires status check to pass before merging` is enabled
	// but no specific checks are declared, it's equivalent
	// to having no status check at all.
	max++
	switch {
	case len(protection.CheckRules.Contexts) > 0:
		info(dl, doLogging, "status check found to merge onto on branch '%s'", branch)
		score++
	default:
		warn(dl, doLogging, "no status checks found to merge onto branch '%s'", branch)
	}
	return score, max
}

func nonAdminReviewProtection(protection *clients.BranchProtectionRule) (int, int) {
	score := 0
	max := 0

	max++
	if protection.RequiredPullRequestReviews.RequiredApprovingReviewCount != nil &&
		*protection.RequiredPullRequestReviews.RequiredApprovingReviewCount > 0 {
		// We do not display anything here, it's done in nonAdminThoroughReviewProtection()
		score++
	}
	return score, max
}

func adminReviewProtection(protection *clients.BranchProtectionRule, branch string,
	dl checker.DetailLogger, doLogging bool) (int, int) {
	score := 0
	max := 0

	if protection.CheckRules.UpToDateBeforeMerge != nil {
		// Note: `This setting will not take effect unless at least one status check is enabled`.
		max++
		switch *protection.CheckRules.UpToDateBeforeMerge {
		case true:
			info(dl, doLogging, "status checks require up-to-date branches for '%s'", branch)
			score++
		default:
			warn(dl, doLogging, "status checks do not require up-to-date branches for '%s'", branch)
		}
	} else {
		debug(dl, doLogging, "unable to retrieve whether up-to-date branches are needed to merge on branch '%s'", branch)
	}

	return score, max
}

func adminThoroughReviewProtection(protection *clients.BranchProtectionRule, branch string,
	dl checker.DetailLogger, doLogging bool) (int, int) {
	score := 0
	max := 0
	if protection.RequiredPullRequestReviews.DismissStaleReviews != nil {
		// Note: we don't inrecase max possible score for non-admin viewers.
		max++
		switch *protection.RequiredPullRequestReviews.DismissStaleReviews {
		case true:
			info(dl, doLogging, "Stale review dismissal enabled on branch '%s'", branch)
			score++
		case false:
			warn(dl, doLogging, "Stale review dismissal disabled on branch '%s'", branch)
		}
	} else {
		debug(dl, doLogging, "unable to retrieve review dismissal on branch '%s'", branch)
	}
	return score, max
}

func nonAdminThoroughReviewProtection(protection *clients.BranchProtectionRule, branch string,
	dl checker.DetailLogger, doLogging bool) (int, int) {
	score := 0
	max := 0

	max++
	if protection.RequiredPullRequestReviews.RequiredApprovingReviewCount != nil {
		switch *protection.RequiredPullRequestReviews.RequiredApprovingReviewCount >= minReviews {
		case true:
			info(dl, doLogging, "number of required reviewers is %d on branch '%s'",
				*protection.RequiredPullRequestReviews.RequiredApprovingReviewCount, branch)
			score++
		default:
			warn(dl, doLogging, "number of required reviewers is only %d on branch '%s'",
				*protection.RequiredPullRequestReviews.RequiredApprovingReviewCount, branch)
		}
	} else {
		warn(dl, doLogging, "number of required reviewers is 0 on branch '%s'", branch)
	}
	return score, max
}

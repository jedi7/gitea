// Copyright 2017 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"net/http"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/models/db"
	"code.gitea.io/gitea/models/organization"
	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/log"
	repo_module "code.gitea.io/gitea/modules/repository"
	"code.gitea.io/gitea/modules/web"
	"code.gitea.io/gitea/services/forms"
	issue_service "code.gitea.io/gitea/services/issue"
)

const (
	tplLabels base.TplName = "repo/issue/labels"
)

// Labels render issue's labels page
func Labels(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.labels")
	ctx.Data["PageIsIssueList"] = true
	ctx.Data["PageIsLabels"] = true
	ctx.Data["RequireTribute"] = true
	ctx.Data["LabelTemplates"] = repo_module.LabelTemplates
	ctx.HTML(http.StatusOK, tplLabels)
}

// InitializeLabels init labels for a repository
func InitializeLabels(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.InitializeLabelsForm)
	if ctx.HasError() {
		ctx.Redirect(ctx.Repo.RepoLink + "/labels")
		return
	}

	if err := repo_module.InitializeLabels(ctx, ctx.Repo.Repository.ID, form.TemplateName, false); err != nil {
		if repo_module.IsErrIssueLabelTemplateLoad(err) {
			originalErr := err.(repo_module.ErrIssueLabelTemplateLoad).OriginalError
			ctx.Flash.Error(ctx.Tr("repo.issues.label_templates.fail_to_load_file", form.TemplateName, originalErr))
			ctx.Redirect(ctx.Repo.RepoLink + "/labels")
			return
		}
		ctx.ServerError("InitializeLabels", err)
		return
	}
	ctx.Redirect(ctx.Repo.RepoLink + "/labels")
}

// RetrieveLabels find all the labels of a repository and organization
func RetrieveLabels(ctx *context.Context) {
	labels, err := models.GetLabelsByRepoID(ctx, ctx.Repo.Repository.ID, ctx.FormString("sort"), db.ListOptions{})
	if err != nil {
		ctx.ServerError("RetrieveLabels.GetLabels", err)
		return
	}

	for _, l := range labels {
		l.CalOpenIssues()
	}

	ctx.Data["Labels"] = labels

	if ctx.Repo.Owner.IsOrganization() {
		orgLabels, err := models.GetLabelsByOrgID(ctx, ctx.Repo.Owner.ID, ctx.FormString("sort"), db.ListOptions{})
		if err != nil {
			ctx.ServerError("GetLabelsByOrgID", err)
			return
		}
		for _, l := range orgLabels {
			l.CalOpenOrgIssues(ctx.Repo.Repository.ID, l.ID)
		}
		ctx.Data["OrgLabels"] = orgLabels

		org, err := organization.GetOrgByName(ctx.Repo.Owner.LowerName)
		if err != nil {
			ctx.ServerError("GetOrgByName", err)
			return
		}
		if ctx.Doer != nil {
			ctx.Org.IsOwner, err = org.IsOwnedBy(ctx.Doer.ID)
			if err != nil {
				ctx.ServerError("org.IsOwnedBy", err)
				return
			}
			ctx.Org.OrgLink = org.AsUser().OrganisationLink()
			ctx.Data["IsOrganizationOwner"] = ctx.Org.IsOwner
			ctx.Data["OrganizationLink"] = ctx.Org.OrgLink
		}
	}
	ctx.Data["NumLabels"] = len(labels)
	ctx.Data["SortType"] = ctx.FormString("sort")
}

// NewLabel create new label for repository
func NewLabel(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.CreateLabelForm)
	ctx.Data["Title"] = ctx.Tr("repo.labels")
	ctx.Data["PageIsLabels"] = true

	if ctx.HasError() {
		ctx.Flash.Error(ctx.Data["ErrorMsg"].(string))
		ctx.Redirect(ctx.Repo.RepoLink + "/labels")
		return
	}

	l := &models.Label{
		RepoID:      ctx.Repo.Repository.ID,
		Name:        form.Title,
		Description: form.Description,
		Color:       form.Color,
	}
	if err := models.NewLabel(ctx, l); err != nil {
		ctx.ServerError("NewLabel", err)
		return
	}
	ctx.Redirect(ctx.Repo.RepoLink + "/labels")
}

// UpdateLabel update a label's name and color
func UpdateLabel(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.CreateLabelForm)
	l, err := models.GetLabelInRepoByID(ctx, ctx.Repo.Repository.ID, form.ID)
	if err != nil {
		switch {
		case models.IsErrRepoLabelNotExist(err):
			ctx.Error(http.StatusNotFound)
		default:
			ctx.ServerError("UpdateLabel", err)
		}
		return
	}

	l.Name = form.Title
	l.Description = form.Description
	l.Color = form.Color
	if err := models.UpdateLabel(l); err != nil {
		ctx.ServerError("UpdateLabel", err)
		return
	}
	ctx.Redirect(ctx.Repo.RepoLink + "/labels")
}

// DeleteLabel delete a label
func DeleteLabel(ctx *context.Context) {
	if err := models.DeleteLabel(ctx.Repo.Repository.ID, ctx.FormInt64("id")); err != nil {
		ctx.Flash.Error("DeleteLabel: " + err.Error())
	} else {
		ctx.Flash.Success(ctx.Tr("repo.issues.label_deletion_success"))
	}

	ctx.JSON(http.StatusOK, map[string]interface{}{
		"redirect": ctx.Repo.RepoLink + "/labels",
	})
}

// UpdateIssueLabel change issue's labels
func UpdateIssueLabel(ctx *context.Context) {
	issues := getActionIssues(ctx)
	if ctx.Written() {
		return
	}

	switch action := ctx.FormString("action"); action {
	case "clear":
		for _, issue := range issues {
			if err := issue_service.ClearLabels(issue, ctx.Doer); err != nil {
				ctx.ServerError("ClearLabels", err)
				return
			}
		}
	case "attach", "detach", "toggle":
		label, err := models.GetLabelByID(ctx, ctx.FormInt64("id"))
		if err != nil {
			if models.IsErrRepoLabelNotExist(err) {
				ctx.Error(http.StatusNotFound, "GetLabelByID")
			} else {
				ctx.ServerError("GetLabelByID", err)
			}
			return
		}

		if action == "toggle" {
			// detach if any issues already have label, otherwise attach
			action = "attach"
			for _, issue := range issues {
				if models.HasIssueLabel(ctx, issue.ID, label.ID) {
					action = "detach"
					break
				}
			}
		}

		if action == "attach" {
			for _, issue := range issues {
				if err = issue_service.AddLabel(issue, ctx.Doer, label); err != nil {
					ctx.ServerError("AddLabel", err)
					return
				}
			}
		} else {
			for _, issue := range issues {
				if err = issue_service.RemoveLabel(issue, ctx.Doer, label); err != nil {
					ctx.ServerError("RemoveLabel", err)
					return
				}
			}
		}
	default:
		log.Warn("Unrecognized action: %s", action)
		ctx.Error(http.StatusInternalServerError)
		return
	}

	ctx.JSON(http.StatusOK, map[string]interface{}{
		"ok": true,
	})
}

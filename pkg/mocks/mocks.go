// Copyright Jetstack Ltd. See LICENSE for details.
package mocks

// This package contains generated mocks

//go:generate mockgen -package=mocks -destination authenticator.go k8s.io/apiserver/pkg/authentication/authenticator Token
//go:generate mockgen -package=mocks -destination subjectaccessreviewer.go k8s.io/api/authorization/v1 SubjectAccessReview

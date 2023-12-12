package server

import "github.com/labstack/echo/v4"

type EchoRouter interface {
	CONNECT(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	DELETE(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	GET(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	HEAD(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	OPTIONS(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	PATCH(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	POST(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	PUT(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
	TRACE(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
}

type Server interface {
	Index(c echo.Context) error
	SubmitJob(c echo.Context) error
	JobDownload(c echo.Context) error
}

func RegisterHandlers(router EchoRouter, si Server, _ ...echo.MiddlewareFunc) {
	router.GET("/", si.Index).Name = "index"
	router.POST("/submit", si.SubmitJob).Name = "submit"
	router.GET("/downloads/:id", si.JobDownload).Name = "job-download"
}

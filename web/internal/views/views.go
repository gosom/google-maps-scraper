package views

import "embed"

// Content holds our static web server content.
//
//go:embed layouts/* templates/* includes/*
var Content embed.FS

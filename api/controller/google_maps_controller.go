package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/gosom/google-maps-scraper/jobs"
	"github.com/gosom/google-maps-scraper/models"
)

func SeedDatabase(c *gin.Context, args models.Arguments) {
	var jsonInput models.JsonInput
	// Bind the JSON request body to the JsonInput struct
	if err := c.BindJSON(&jsonInput); err != nil {
		c.AbortWithError(400, err)
		return
	}
	args.ProduceOnly = true
	args.InputFile = "json"
	err := jobs.RunFromDatabase(c.Request.Context(), &args, &jsonInput)
	if err != nil {
		c.AbortWithError(500, err)
		return
	}
	c.JSON(200, models.NewResponseDto(true))
}

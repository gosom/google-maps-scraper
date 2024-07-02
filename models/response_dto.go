package models

type ResponseDto struct {
	Success bool              `json:"success"`
	Result  interface{}       `json:"result"`
	Error   *ErrorResponseDto `json:"error"`
}

type ErrorResponseDto struct {
	Message string                    `json:"message"`
	Code    string                    `json:"code"`
	Details []*ErrorDetailResponseDto `json:"details"`
}
type ErrorDetailResponseDto struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func NewResponseDto(data interface{}) ResponseDto {
	return ResponseDto{
		Success: true,
		Result:  data,
		Error:   nil,
	}
}
func NewErrorResponseDto(err error) ResponseDto {
	return ResponseDto{
		Success: false,
		Result:  nil,
		Error:   &ErrorResponseDto{Message: err.Error()},
	}
}
func NewErrorResponseDtoMessage(message string) ResponseDto {
	return ResponseDto{
		Success: false,
		Result:  nil,
		Error:   &ErrorResponseDto{Message: message},
	}
}

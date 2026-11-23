package api

// UploadSignRequest is the body of POST /uploads/sign.
type UploadSignRequest struct {
	// Purpose picks the folder: profile, progress, meals or library.
	Purpose string `json:"purpose"`
	// Size is the file size in bytes, counted against the file_manager quota.
	Size int64 `json:"size"`
}

// UploadSignature is what the client posts to Cloudinary as multipart fields, next to "file".
type UploadSignature struct {
	UploadURL string            `json:"upload_url"`
	Fields    map[string]string `json:"fields"`
	// ExpiresAt is when Cloudinary stops accepting the signature (Unix seconds).
	ExpiresAt int64 `json:"expires_at"`
}

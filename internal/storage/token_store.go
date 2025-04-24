package storage

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TokenStore defines the interface for token storage operations
type TokenStore interface {
	GetToken(userID string) (string, error)
	SetToken(userID, token string) error
}

// S3TokenStore implements TokenStore using AWS S3
type S3TokenStore struct {
	client     *s3.Client
	bucketName string
	encryptKey []byte // 32-byte key for AES-256
}

type tokenData struct {
	Token string `json:"token"`
}

// NewS3TokenStore creates a new S3TokenStore instance
func NewS3TokenStore(client *s3.Client, bucketName string, encryptKey []byte) *S3TokenStore {
	return &S3TokenStore{
		client:     client,
		bucketName: bucketName,
		encryptKey: encryptKey,
	}
}

// GetToken retrieves and decrypts a token for the given user ID
func (s *S3TokenStore) GetToken(userID string) (string, error) {
	key := s.getKey(userID)

	result, err := s.client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", fmt.Errorf("failed to get token from S3: %v", err)
	}
	defer result.Body.Close()

	var data tokenData
	if err := json.NewDecoder(result.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("failed to decode token data: %v", err)
	}

	// Decrypt the token
	decryptedToken, err := s.decrypt(data.Token)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt token: %v", err)
	}

	return decryptedToken, nil
}

// SetToken encrypts and stores a token for the given user ID
func (s *S3TokenStore) SetToken(userID, token string) error {
	key := s.getKey(userID)

	// Encrypt the token
	encryptedToken, err := s.encrypt(token)
	if err != nil {
		return fmt.Errorf("failed to encrypt token: %v", err)
	}

	data := tokenData{Token: encryptedToken}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal token data: %v", err)
	}

	_, err = s.client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
		Body:   bytes.NewReader(jsonData),
	})
	if err != nil {
		return fmt.Errorf("failed to store token in S3: %v", err)
	}

	return nil
}

// encrypt encrypts the token using AES-GCM
func (s *S3TokenStore) encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(s.encryptKey)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	// Generate a random nonce
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	// Encrypt the data
	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)

	// Encode the result in base64
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decrypt decrypts the token using AES-GCM
func (s *S3TokenStore) decrypt(encryptedText string) (string, error) {
	// Decode the base64 string
	ciphertext, err := base64.StdEncoding.DecodeString(encryptedText)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(s.encryptKey)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	if len(ciphertext) < aesGCM.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}

	// Split nonce and ciphertext
	nonce := ciphertext[:aesGCM.NonceSize()]
	ciphertext = ciphertext[aesGCM.NonceSize():]

	// Decrypt the data
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// getKey generates the S3 key for a user's token
func (s *S3TokenStore) getKey(userID string) string {
	return fmt.Sprintf("tokens/%s.json", userID)
}

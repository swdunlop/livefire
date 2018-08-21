package tarantula

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"
)

/* Seal Structure:
    |---- mac ----|            |------|
            |--------- aes-ctr -------| 
    [ iv ]  [ exp ]  [ mac ]   [ data ]
   0      16       24        56         56 + len(data)
    |---------------- seal -----------|

    - modifying the iv results in a mac mismatch
    - modifying the ciphertext results in a mac mismatch
    - identifying the mac secret requires finding the ciphertext key
    - ctr is not considered vulnerable to known plaintext attack
*/

// Seal uses SHA256 in a HMAC configuration to authenticate data, and then encrypts it using AES-CTR with a random IV.  This protects the
// sealed data from modification or analysis by the recipient.  An expiry time is also sealed into the data structure, limiting the viability
// of the seal.
func Seal(auth, key, data []byte, exp time.Time) ([]byte, error) {
	hash := hmac.New(sha256.New, auth)
	seal := make([]byte, len(data)+56)

	// Read our IV
	_, err := rand.Read(seal[:16])
	if err != nil {
		return nil, err
	}

	// Apply our Stamp
	binary.LittleEndian.PutUint64(seal[16:], uint64(exp.Unix()))

	// Copy in our plaintext.
	copy(seal[56:], data)

	// HMAC everything before and after the HMAC field.
	hash.Write(seal[0:24])
	hash.Write(seal[56:])
	copy(seal[24:56], hash.Sum(nil))

	// Encrypt our HMAC and PLAINTEXT
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	cipher.NewCTR(b, seal[:16]).XORKeyStream(seal[16:], seal[16:])

	return seal, nil
}

// Unseal reverses the operations in Seal to decrypt the enclosed data, verify its integrity and checks timeliness against time.Now().  The error
// result MUST BE CHECKED in all cases, as Unseal will always return what it believes to be the decrypted but not authenticated data.
func Unseal(auth, key, seal []byte) ([]byte, error) {
	if len(seal) < 56 {
		return seal, SEAL_UNDERFLOW
	}

	// We copy off the seal because we're about to manipulate it destructively.
	seal = append(make([]byte, 0, len(seal)), seal...)
	hash := hmac.New(sha256.New, auth)

	// Decrypt our HMAC and PLAINTEXT
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	cipher.NewCTR(b, seal[:16]).XORKeyStream(seal[16:], seal[16:])

	// Extract our Timestamp
	expiry := binary.LittleEndian.Uint64(seal[16:24])

	// Compare our HMAC
	hash.Write(seal[0:24])
	hash.Write(seal[56:])
	if bytes.Compare(seal[24:56], hash.Sum(nil)) != 0 {
		return nil, SEAL_MISMATCH
	}

	// Compare our expiry
	if time.Now().After(time.Unix(int64(expiry), 0)) {
		return nil, SEAL_EXPIRED
	}

	return seal[56:], nil
}

var SEAL_UNDERFLOW = errors.New("sealed data too small to be valid")
var SEAL_MISMATCH = errors.New("sealed hmac did not match")
var SEAL_EXPIRED = errors.New("sealed data has expired")

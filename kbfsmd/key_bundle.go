// Copyright 2017 Keybase Inc. All rights reserved.
// Use of this source code is governed by a BSD
// license that can be found in the LICENSE file.

package kbfsmd

import (
	"fmt"

	"github.com/keybase/client/go/protocol/keybase1"
	"github.com/keybase/go-codec/codec"
	"github.com/keybase/kbfs/kbfscrypto"
	"github.com/keybase/kbfs/kbfshash"
)

// TLFCryptKeyServerHalfID is the identifier type for a server-side key half.
type TLFCryptKeyServerHalfID struct {
	ID kbfshash.HMAC // Exported for serialization.
}

// String implements the Stringer interface for TLFCryptKeyServerHalfID.
func (id TLFCryptKeyServerHalfID) String() string {
	return id.ID.String()
}

// TLFCryptKeyInfo is a per-device key half entry in the
// TLF{Writer,Reader}KeyBundleV{2,3}.
type TLFCryptKeyInfo struct {
	ClientHalf   EncryptedTLFCryptKeyClientHalf
	ServerHalfID TLFCryptKeyServerHalfID
	EPubKeyIndex int `codec:"i,omitempty"`

	codec.UnknownFieldSetHandler
}

// DevicePublicKeys is a set of a user's devices (identified by the
// corresponding device CryptPublicKey).
type DevicePublicKeys map[kbfscrypto.CryptPublicKey]bool

// UserDevicePublicKeys is a map from users to that user's set of devices.
type UserDevicePublicKeys map[keybase1.UID]DevicePublicKeys

// Equals returns whether both sets of keys are equal.
func (dpk DevicePublicKeys) Equals(other DevicePublicKeys) bool {
	if len(dpk) != len(other) {
		return false
	}

	for k := range dpk {
		if !other[k] {
			return false
		}
	}

	return true
}

// DeviceKeyServerHalves is a map from a user devices (identified by the
// corresponding device CryptPublicKey) to corresponding key server
// halves.
type DeviceKeyServerHalves map[kbfscrypto.CryptPublicKey]kbfscrypto.TLFCryptKeyServerHalf

// UserDeviceKeyServerHalves maps a user's keybase UID to their
// DeviceServerHalves map.
type UserDeviceKeyServerHalves map[keybase1.UID]DeviceKeyServerHalves

// cryptoPure contains all methods of Crypto that don't depend on
// implicit state, i.e. they're pure functions of the input.
type cryptoPure interface {
	// MakeRandomTLFCryptKeyServerHalf generates the server-side of a
	// top-level folder crypt key.
	MakeRandomTLFCryptKeyServerHalf() (
		kbfscrypto.TLFCryptKeyServerHalf, error)

	// EncryptTLFCryptKeyClientHalf encrypts a TLFCryptKeyClientHalf
	// using both a TLF's ephemeral private key and a device pubkey.
	EncryptTLFCryptKeyClientHalf(
		privateKey kbfscrypto.TLFEphemeralPrivateKey,
		publicKey kbfscrypto.CryptPublicKey,
		clientHalf kbfscrypto.TLFCryptKeyClientHalf) (
		EncryptedTLFCryptKeyClientHalf, error)

	// GetTLFCryptKeyServerHalfID creates a unique ID for this particular
	// kbfscrypto.TLFCryptKeyServerHalf.
	GetTLFCryptKeyServerHalfID(
		user keybase1.UID, devicePubKey kbfscrypto.CryptPublicKey,
		serverHalf kbfscrypto.TLFCryptKeyServerHalf) (
		TLFCryptKeyServerHalfID, error)
}

// SplitTLFCryptKey splits the given TLFCryptKey into two parts -- the
// client-side part (which is encrypted with the given keys), and the
// server-side part, which will be uploaded to the server.
func SplitTLFCryptKey(crypto cryptoPure, uid keybase1.UID,
	tlfCryptKey kbfscrypto.TLFCryptKey,
	ePrivKey kbfscrypto.TLFEphemeralPrivateKey, ePubIndex int,
	pubKey kbfscrypto.CryptPublicKey) (
	TLFCryptKeyInfo, kbfscrypto.TLFCryptKeyServerHalf, error) {
	//    * create a new random server half
	//    * mask it with the key to get the client half
	//    * encrypt the client half
	var serverHalf kbfscrypto.TLFCryptKeyServerHalf
	serverHalf, err := crypto.MakeRandomTLFCryptKeyServerHalf()
	if err != nil {
		return TLFCryptKeyInfo{}, kbfscrypto.TLFCryptKeyServerHalf{}, err
	}

	clientHalf := kbfscrypto.MaskTLFCryptKey(serverHalf, tlfCryptKey)

	var encryptedClientHalf EncryptedTLFCryptKeyClientHalf
	encryptedClientHalf, err =
		crypto.EncryptTLFCryptKeyClientHalf(ePrivKey, pubKey, clientHalf)
	if err != nil {
		return TLFCryptKeyInfo{}, kbfscrypto.TLFCryptKeyServerHalf{}, err
	}

	var serverHalfID TLFCryptKeyServerHalfID
	serverHalfID, err =
		crypto.GetTLFCryptKeyServerHalfID(uid, pubKey, serverHalf)
	if err != nil {
		return TLFCryptKeyInfo{}, kbfscrypto.TLFCryptKeyServerHalf{}, err
	}

	clientInfo := TLFCryptKeyInfo{
		ClientHalf:   encryptedClientHalf,
		ServerHalfID: serverHalfID,
		EPubKeyIndex: ePubIndex,
	}
	return clientInfo, serverHalf, nil
}

// RemoveKeylessUsersForTest returns a new UserDevicePublicKeys objects with
// all the users with an empty DevicePublicKeys removed.
func (udpk UserDevicePublicKeys) RemoveKeylessUsersForTest() UserDevicePublicKeys {
	udpkRemoved := make(UserDevicePublicKeys)
	for u, dpk := range udpk {
		if len(dpk) > 0 {
			udpkRemoved[u] = dpk
		}
	}
	return udpkRemoved
}

// Equals returns whether both sets of users are equal, and they all
// have corresponding equal sets of keys.
func (udpk UserDevicePublicKeys) Equals(other UserDevicePublicKeys) bool {
	if len(udpk) != len(other) {
		return false
	}

	for u, dpk := range udpk {
		if !dpk.Equals(other[u]) {
			return false
		}
	}

	return true
}

// MergeUsers returns a UserDeviceKeyServerHalves that contains all
// the users in serverHalves and other, which must be disjoint. This
// isn't a deep copy.
func (serverHalves UserDeviceKeyServerHalves) MergeUsers(
	other UserDeviceKeyServerHalves) (UserDeviceKeyServerHalves, error) {
	merged := make(UserDeviceKeyServerHalves,
		len(serverHalves)+len(other))
	for uid, deviceServerHalves := range serverHalves {
		merged[uid] = deviceServerHalves
	}
	for uid, deviceServerHalves := range other {
		if _, ok := merged[uid]; ok {
			return nil, fmt.Errorf(
				"user %s is in both UserDeviceKeyServerHalves",
				uid)
		}
		merged[uid] = deviceServerHalves
	}
	return merged, nil
}

type DeviceServerHalfRemovalInfo map[kbfscrypto.CryptPublicKey][]TLFCryptKeyServerHalfID

// userServerHalfRemovalInfo contains a map from devices (identified
// by its crypt public key) to a list of IDs for key server halves to
// remove (one per key generation). For logging purposes, it also
// contains a bool indicating whether all of the user's devices were
// removed.
type UserServerHalfRemovalInfo struct {
	UserRemoved         bool
	DeviceServerHalfIDs DeviceServerHalfRemovalInfo
}

// addGeneration merges the keys in genInfo (which must be one per
// device) into ri. genInfo must have the same userRemoved value and
// keys as ri.
func (ri UserServerHalfRemovalInfo) addGeneration(
	uid keybase1.UID, genInfo UserServerHalfRemovalInfo) error {
	if ri.UserRemoved != genInfo.UserRemoved {
		return fmt.Errorf(
			"UserRemoved=%t != generation UserRemoved=%t for user %s",
			ri.UserRemoved, genInfo.UserRemoved, uid)
	}

	if len(ri.DeviceServerHalfIDs) != len(genInfo.DeviceServerHalfIDs) {
		return fmt.Errorf(
			"device count=%d != generation device count=%d for user %s",
			len(ri.DeviceServerHalfIDs),
			len(genInfo.DeviceServerHalfIDs), uid)
	}

	idCount := -1
	for key, serverHalfIDs := range genInfo.DeviceServerHalfIDs {
		if idCount == -1 {
			idCount = len(ri.DeviceServerHalfIDs[key])
		} else {
			localIDCount := len(ri.DeviceServerHalfIDs[key])
			if localIDCount != idCount {
				return fmt.Errorf(
					"expected %d keys, got %d for user %s and device %s",
					idCount, localIDCount, uid, key)
			}
		}

		if len(serverHalfIDs) != 1 {
			return fmt.Errorf(
				"expected exactly one key, got %d for user %s and device %s",
				len(serverHalfIDs), uid, key)
		}
		if _, ok := ri.DeviceServerHalfIDs[key]; !ok {
			return fmt.Errorf(
				"no generation info for user %s and device %s",
				uid, key)
		}
		ri.DeviceServerHalfIDs[key] = append(
			ri.DeviceServerHalfIDs[key], serverHalfIDs[0])
	}

	return nil
}

// ServerHalfRemovalInfo is a map from users and devices to a list of
// server half IDs to remove from the server.
type ServerHalfRemovalInfo map[keybase1.UID]UserServerHalfRemovalInfo

// AddGeneration merges the keys in genInfo (which must be one per
// device) into info. genInfo must have the same users as info.
func (info ServerHalfRemovalInfo) AddGeneration(
	genInfo ServerHalfRemovalInfo) error {
	if len(info) != len(genInfo) {
		return fmt.Errorf(
			"user count=%d != generation user count=%d",
			len(info), len(genInfo))
	}

	for uid, removalInfo := range genInfo {
		if _, ok := info[uid]; !ok {
			return fmt.Errorf("no generation info for user %s", uid)
		}
		err := info[uid].addGeneration(uid, removalInfo)
		if err != nil {
			return err
		}
	}
	return nil
}

// MergeUsers returns a ServerHalfRemovalInfo that contains all the
// users in info and other, which must be disjoint. This isn't a deep
// copy.
func (info ServerHalfRemovalInfo) MergeUsers(
	other ServerHalfRemovalInfo) (ServerHalfRemovalInfo, error) {
	merged := make(ServerHalfRemovalInfo, len(info)+len(other))
	for uid, removalInfo := range info {
		merged[uid] = removalInfo
	}
	for uid, removalInfo := range other {
		if _, ok := merged[uid]; ok {
			return nil, fmt.Errorf(
				"user %s is in both ServerHalfRemovalInfos",
				uid)
		}
		merged[uid] = removalInfo
	}
	return merged, nil
}
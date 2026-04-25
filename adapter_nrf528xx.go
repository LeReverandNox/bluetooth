//go:build (softdevice && s113v7) || (softdevice && s132v6) || (softdevice && s140v6) || (softdevice && s140v7)

package bluetooth

// This file defines the SoftDevice adapter for all nrf52-series chips.

/*
#include "nrf_sdm.h"
#include "nrf_nvic.h"
#include "ble.h"
#include "ble_gap.h"
#include "ble_gatts.h"
#include <string.h>

void assertHandler(void);

// configure_attr_tab_size sets the GATTS attribute table size in bytes.
// The nRF52 SoftDevice default is 0x580 (1408 B). Applications that register
// services with many characteristics and CCCDs need a larger table; each
// characteristic value consumes max_len bytes and each CCCD takes ~6 bytes.
// Must be called after sd_softdevice_enable() and before sd_ble_enable().
static void configure_attr_tab_size(uint32_t ram_base, uint32_t sz) {
	ble_cfg_t cfg;
	memset(&cfg, 0, sizeof(cfg));
	cfg.gatts_cfg.attr_tab_size.attr_tab_size = sz;
	sd_ble_cfg_set(BLE_GATTS_CFG_ATTR_TAB_SIZE, &cfg, ram_base);
}

// configure_hvx_queue_size sets the HVX (Handle Value Notification/Indication)
// TX queue depth per connection. The SoftDevice default is 1, meaning
// sd_ble_gatts_hvx() returns NRF_ERROR_RESOURCES if called again before the
// first BLE_GATTS_EVT_HVN_TX_COMPLETE event. Larger values allow back-to-back
// notifications without stalling. RAM cost: n × att_mtu bytes per connection.
// Must be called after sd_softdevice_enable() and before sd_ble_enable().
static void configure_hvx_queue_size(uint32_t ram_base, uint8_t size) {
	ble_cfg_t cfg;
	memset(&cfg, 0, sizeof(cfg));
	cfg.conn_cfg.conn_cfg_tag                            = BLE_CONN_CFG_TAG_DEFAULT;
	cfg.conn_cfg.params.gatts_conn_cfg.hvn_tx_queue_size = size;
	sd_ble_cfg_set(BLE_CONN_CFG_GATTS, &cfg, ram_base);
}
*/
import "C"

import (
	"machine"
	"unsafe"
)

//export assertHandler
func assertHandler() {
	println("SoftDevice assert")
}

var clockConfigXtal C.nrf_clock_lf_cfg_t = C.nrf_clock_lf_cfg_t{
	source:       C.NRF_CLOCK_LF_SRC_XTAL,
	rc_ctiv:      0,
	rc_temp_ctiv: 0,
	accuracy:     C.NRF_CLOCK_LF_ACCURACY_250_PPM,
}

//go:extern __app_ram_base
var appRAMBase [0]uint32

// sdCfgAttrTabSize, sdCfgHVXQueueSize, and sdCfgCharMaxLen hold values
// requested via SetGATTSAttrTabSize, SetHVXQueueSize, and
// SetCharacteristicMaxLen respectively. A zero value means "use the
// SoftDevice / library default".
// AttrTabSize and HVXQueueSize are applied in enable() via sd_ble_cfg_set,
// which must be called after sd_softdevice_enable() and before sd_ble_enable().
// CharMaxLen is read directly by AddService() and has no timing constraint.
var (
	sdCfgAttrTabSize  uint32
	sdCfgHVXQueueSize uint8
	sdCfgCharMaxLen   uint16
)

func (a *Adapter) enable() error {
	// Enable the SoftDevice.
	var clockConfig *C.nrf_clock_lf_cfg_t
	if machine.HasLowFrequencyCrystal {
		clockConfig = &clockConfigXtal
	}
	errCode := C.sd_softdevice_enable(clockConfig, C.nrf_fault_handler_t(C.assertHandler))
	if errCode != 0 {
		return Error(errCode)
	}

	// Apply any sd_ble_cfg_set calls requested via Set* methods. This is the
	// only valid window: after sd_softdevice_enable(), before sd_ble_enable().
	ram := C.uint32_t(uintptr(unsafe.Pointer(&appRAMBase)))
	if sdCfgAttrTabSize > 0 {
		C.configure_attr_tab_size(ram, C.uint32_t(sdCfgAttrTabSize))
	}
	if sdCfgHVXQueueSize > 0 {
		C.configure_hvx_queue_size(ram, C.uint8_t(sdCfgHVXQueueSize))
	}

	// Enable the BLE stack.
	errCode = C.sd_ble_enable(&ram)
	return makeError(errCode)
}

// SetGATTSAttrTabSize configures the GATTS attribute table size before
// Enable() is called. The SoftDevice default is 0x580 (1408 B). Increase
// this when registering services with many characteristics: each
// characteristic value stored in the attribute table (BLE_GATTS_VLOC_STACK)
// consumes max_len bytes, and each CCCD (Client Characteristic Configuration
// Descriptor) takes approximately 6 bytes. A service with four characteristics
// and CCCDs may need 0xC00 (3072 B) or more.
//
// This method has no effect if called after Enable().
func (a *Adapter) SetGATTSAttrTabSize(sz uint32) {
	sdCfgAttrTabSize = sz
}

// SetHVXQueueSize configures the HVX (Handle Value Notification/Indication)
// TX queue depth per connection before Enable() is called. The SoftDevice
// default is 1, which means sd_ble_gatts_hvx() returns NRF_ERROR_RESOURCES
// if a second notification is queued before the previous
// BLE_GATTS_EVT_HVN_TX_COMPLETE event fires. Increase this when sending
// multi-chunk notifications (splitting a large payload across multiple ATT
// notifications). RAM cost at the default ATT MTU (23 bytes): n × 23 bytes
// per connection.
//
// This method has no effect if called after Enable().
func (a *Adapter) SetHVXQueueSize(n uint8) {
	sdCfgHVXQueueSize = n
}

// SetCharacteristicMaxLen sets the maximum value length (in bytes) allocated
// per characteristic in the GATTS attribute table. The default is 20, which
// matches the legacy Bluetooth 4.0 ATT payload limit.
//
// With BLE_GATTS_VLOC_STACK (used by this library), the SoftDevice
// pre-allocates exactly max_len bytes per characteristic in the attribute
// table, so increasing this value increases attribute table memory usage.
// Applications that use Data Length Extension (DLE) and want to receive or
// send characteristic values larger than 20 bytes should set this to 244
// (the maximum ATT payload with DLE: ATT MTU 247 − 3-byte header) and
// call SetGATTSAttrTabSize to enlarge the attribute table accordingly.
//
// This method may be called at any time before AddService().
func (a *Adapter) SetCharacteristicMaxLen(size uint16) {
	sdCfgCharMaxLen = size
}

func (a *Adapter) Address() (MACAddress, error) {
	var addr C.ble_gap_addr_t
	errCode := C.sd_ble_gap_addr_get(&addr)
	if errCode != 0 {
		return MACAddress{}, Error(errCode)
	}
	return MACAddress{MAC: makeAddress(addr.addr)}, nil
}

// Convert a C.ble_gap_addr_t to a MACAddress struct.
func makeMACAddress(addr C.ble_gap_addr_t) MACAddress {
	return MACAddress{
		MAC:      makeAddress(addr.addr),
		isRandom: addr.bitfield_addr_type() != 0,
	}
}

// Always let the BLE stack pick the right PHY.
var phyUpdateResponse = C.ble_gap_phys_t{
	tx_phys: C.BLE_GAP_PHY_AUTO,
	rx_phys: C.BLE_GAP_PHY_AUTO,
}

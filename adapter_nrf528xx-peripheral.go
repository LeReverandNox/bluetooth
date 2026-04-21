//go:build softdevice && s113v7

package bluetooth

// This file implements the event handler for SoftDevices with only peripheral
// mode support. This includes the S113.

/*
#include "nrf_sdm.h"
#include "nrf_nvic.h"
#include "ble.h"
#include "ble_gap.h"
#include <string.h>

// reply_sec_params_just_works responds to BLE_GAP_EVT_SEC_PARAMS_REQUEST with
// "Just Works" pairing (no bonding, no MITM, IO caps = None). The entire reply
// is handled in C to avoid TinyGo allocating the ble_gap_sec_params_t struct
// on the heap, which would panic with "heap alloc in interrupt".
static void reply_sec_params_just_works(uint16_t conn_handle) {
    ble_gap_sec_params_t sec_params;
    memset(&sec_params, 0, sizeof(sec_params));
    sec_params.bond         = 0;
    sec_params.mitm         = 0;
    sec_params.lesc         = 0;
    sec_params.keypress     = 0;
    sec_params.io_caps      = BLE_GAP_IO_CAPS_NONE;
    sec_params.oob          = 0;
    sec_params.min_key_size = 7;
    sec_params.max_key_size = 16;
    // keyset is NULL because bond=0; the SoftDevice ignores it in that case.
    sd_ble_gap_sec_params_reply(conn_handle, BLE_GAP_SEC_STATUS_SUCCESS, &sec_params, NULL);
}
*/
import "C"

import (
	"unsafe"
)

func handleEvent() {
	id := eventBuf.header.evt_id
	switch {
	case id >= C.BLE_GAP_EVT_BASE && id <= C.BLE_GAP_EVT_LAST:
		gapEvent := eventBuf.evt.unionfield_gap_evt()
		switch id {
		case C.BLE_GAP_EVT_CONNECTED:
			if debug {
				println("evt: connected in peripheral role")
			}
			currentConnection.handle.Reg = uint16(gapEvent.conn_handle)
			// Initialise system attributes (including CCCDs) immediately on
			// connect. Without this, sd_ble_gatts_hvx returns
			// BLE_ERROR_GATTS_SYS_ATTR_MISSING (0x3401) for every notification
			// until the central triggers BLE_GATTS_EVT_SYS_ATTR_MISSING by
			// performing an ATT operation — which may never happen if the
			// peripheral notifies before the central reads anything.
			C.sd_ble_gatts_sys_attr_set(gapEvent.conn_handle, nil, 0, 0)
			connectEvent := gapEvent.params.unionfield_connected()
			device := Device{
				Address:          Address{makeMACAddress(connectEvent.peer_addr)},
				connectionHandle: gapEvent.conn_handle,
			}
			DefaultAdapter.connectHandler(device, true)
		case C.BLE_GAP_EVT_DISCONNECTED:
			if debug {
				println("evt: disconnected")
			}
			currentConnection.handle.Reg = C.BLE_CONN_HANDLE_INVALID
			// Auto-restart advertisement if needed.
			if defaultAdvertisement.isAdvertising.Get() != 0 {
				// The advertisement was running but was automatically stopped
				// by the connection event.
				// Note that it cannot be restarted during connect like this,
				// because it would need to be reconfigured as a non-connectable
				// advertisement. That's left as a future addition, if
				// necessary.
				C.sd_ble_gap_adv_start(defaultAdvertisement.handle, C.BLE_CONN_CFG_TAG_DEFAULT)
			}
			device := Device{
				connectionHandle: gapEvent.conn_handle,
			}
			DefaultAdapter.connectHandler(device, false)
		case C.BLE_GAP_EVT_DATA_LENGTH_UPDATE_REQUEST:
			// We need to respond with sd_ble_gap_data_length_update. Setting
			// both parameters to nil will make sure we send the default values.
			C.sd_ble_gap_data_length_update(gapEvent.conn_handle, nil, nil)
		case C.BLE_GAP_EVT_DATA_LENGTH_UPDATE:
			// ignore confirmation of data length successfully updated
		case C.BLE_GAP_EVT_PHY_UPDATE_REQUEST:
			// Tell the Bluetooth stack to update the PHY as it sees fit.
			C.sd_ble_gap_phy_update(gapEvent.conn_handle, &phyUpdateResponse)
		case C.BLE_GAP_EVT_PHY_UPDATE:
			// ignore confirmation of phy successfully updated
		case C.BLE_GAP_EVT_SEC_PARAMS_REQUEST:
			// The peer (central) wants to pair. The reply is handled in C via
			// reply_sec_params_just_works() to avoid TinyGo allocating
			// ble_gap_sec_params_t on the heap, which would panic with
			// "heap alloc in interrupt". Without this reply, the SoftDevice
			// times out and sends SMP Pairing Failed; BlueZ surfaces this as
			// "Authentication Canceled".
			if debug {
				println("evt: sec_params_request — replying with Just Works")
			}
			C.reply_sec_params_just_works(gapEvent.conn_handle)
		case C.BLE_GAP_EVT_AUTH_STATUS:
			// Pairing/bonding procedure finished. No action required for
			// Just Works without bonding.
			if debug {
				authStatus := gapEvent.params.unionfield_auth_status()
				println("evt: auth_status =", authStatus.auth_status)
			}
		default:
			if debug {
				println("unknown GAP event:", id)
			}
		}
	case id >= C.BLE_GATTS_EVT_BASE && id <= C.BLE_GATTS_EVT_LAST:
		gattsEvent := eventBuf.evt.unionfield_gatts_evt()
		switch id {
		case C.BLE_GATTS_EVT_WRITE:
			writeEvent := gattsEvent.params.unionfield_write()
			len := writeEvent.len - writeEvent.offset
			data := (*[255]byte)(unsafe.Pointer(&writeEvent.data[0]))[:len:len]
			handler := DefaultAdapter.getCharWriteHandler(writeEvent.handle)
			if handler != nil {
				handler.callback(Connection(gattsEvent.conn_handle), int(writeEvent.offset), data)
			}
		case C.BLE_GATTS_EVT_SYS_ATTR_MISSING:
			// This event is generated when reading the Generic Attribute
			// service. It appears to be necessary for bonded devices.
			// From the docs:
			// > If the pointer is NULL, the system attribute info is
			// > initialized, assuming that the application does not have any
			// > previously saved system attribute data for this device.
			// Maybe we should look at the error, but as there's not really a
			// way to handle it, ignore it.
			C.sd_ble_gatts_sys_attr_set(gattsEvent.conn_handle, nil, 0, 0)
		case C.BLE_GATTS_EVT_EXCHANGE_MTU_REQUEST:
			// This event is generated by some devices. While we could support
			// larger MTUs, this default MTU is supported everywhere.
			C.sd_ble_gatts_exchange_mtu_reply(gattsEvent.conn_handle, C.BLE_GATT_ATT_MTU_DEFAULT)
		case C.BLE_GATTS_EVT_HVN_TX_COMPLETE:
			// ignore confirmation of a notification successfully sent
		default:
			if debug {
				println("unknown GATTS event:", id, id-C.BLE_GATTS_EVT_BASE)
			}
		}
	default:
		if debug {
			println("unknown event:", id)
		}
	}
}

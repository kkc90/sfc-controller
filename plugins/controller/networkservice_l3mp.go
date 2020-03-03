// Copyright (c) 2017 Cisco and/or its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"fmt"

	controller "github.com/ligato/sfc-controller/plugins/controller/model"
	"github.com/ligato/sfc-controller/plugins/controller/vppagent"
	l2 "go.ligato.io/vpp-agent/v3/proto/ligato/vpp/l2"
)

// The L3MP topology is rendered in this module for a connection with a vnf-service

// RenderConnL3MP renders this L3MP connection
func (mgr *NetworkServiceMgr) RenderConnL3MP(
	ns *controller.NetworkService,
	conn *controller.Connection,
	connIndex uint32) error {

	// need to order these to match the array of the conn's conn.PodInterfaces
	var p2nArray []controller.NetworkPodToNodeMap
	netPodInterfaces := make([]*controller.Interface, 0)
	networkPodTypes := make([]string, 0)

	allPodsAssignedToNodes := true
	staticNodesInInterfacesSpecified := false
	var nodeMap = make(map[string]bool, 0) // determine the set of nodes

	log.Debugf("RenderConnL3MP: num pod interfaces: %d", len(conn.PodInterfaces))
	log.Debugf("RenderConnL3MP: num node interfaces: %d", len(conn.NodeInterfaces))
	log.Debugf("RenderConnL3MP: num node labels: %d", len(conn.NodeInterfaceLabels))

	// let's see if all interfaces in the conn are associated with a node
	for connIndex, connPodInterface := range conn.PodInterfaces {

		connPodName, connInterfaceName := ConnPodInterfaceNames(connPodInterface)

		p2n, exists := ctlrPlugin.ramCache.NetworkPodToNodeMap[connPodName]
		if !exists || p2n.Node == "" {
			msg := fmt.Sprintf("conn: %d: %s, network pod not mapped to a node in network-pod-to-node-map",
				connIndex+1, connPodInterface)
			mgr.AppendStatusMsg(ns, msg)
			allPodsAssignedToNodes = false
			continue
		}

		_, exists = ctlrPlugin.NetworkNodeMgr.HandleCRUDOperationR(p2n.Node)
		if !exists {
			msg := fmt.Sprintf("conn: %d: %s, network pod references non existant host: %s",
				connIndex+1, connPodInterface, p2n.Node)
			mgr.AppendStatusMsg(ns, msg)
			allPodsAssignedToNodes = false
			continue
		}

		nodeMap[p2n.Node] = true // maintain a map of which nodes are in the conn set

		// based on the interfaces in the conn, order the interface info accordingly as the set of
		// interfaces in the pod/interface stanza can be in a different order
		p2nArray = append(p2nArray, *p2n)
		netPodInterface, networkPodType := mgr.findNetworkPodAndInterfaceInList(
			ns, connPodName, connInterfaceName, ns.Spec.NetworkPods)
		netPodInterface.Parent = connPodName
		netPodInterfaces = append(netPodInterfaces, netPodInterface)
		networkPodTypes = append(networkPodTypes, networkPodType)
	}

	for _, nodeInterface := range conn.NodeInterfaces {

		connNodeName, connInterfaceName := NodeInterfaceNames(nodeInterface)

		nodeInterface, nodeIfType := ctlrPlugin.NetworkNodeMgr.FindInterfaceInNode(connNodeName, connInterfaceName)
		nodeInterface.Parent = connNodeName
		var p2n controller.NetworkPodToNodeMap
		p2n.Node = connNodeName
		p2n.Pod = connNodeName
		p2nArray = append(p2nArray, p2n)
		netPodInterfaces = append(netPodInterfaces, nodeInterface)
		networkPodTypes = append(networkPodTypes, nodeIfType)
		staticNodesInInterfacesSpecified = true

		nodeMap[connNodeName] = true // maintain a map of which nodes are in the conn set
	}

	if !allPodsAssignedToNodes {
		msg := fmt.Sprintf("network-service: %s, not all network pods in this connection are mapped to nodes",
			ns.Metadata.Name)
		mgr.AppendStatusMsg(ns, msg)
		return fmt.Errorf(msg)
	}

	log.Debugf("RenderTopologyL3MP: num unique nodes for this connection: %d", len(nodeMap))
	// log.Debugf("RenderTopologyL3MP: p2n=%v, vnfI=%v, conn=%v", p2n, netPodInterfaces, conn)

	// if an overlay is specified, see if it exists
	var nno *controller.NetworkNodeOverlay
	exists := false
	if conn.NetworkNodeOverlayName != "" {
		nno, exists = ctlrPlugin.NetworkNodeOverlayMgr.HandleCRUDOperationR(conn.NetworkNodeOverlayName)
		if !exists {
			msg := fmt.Sprintf("network-service: %s, conn: %d, referencing a missing overlay: %s",
				ns.Metadata.Name,
				connIndex+1,
				conn.NetworkNodeOverlayName)
			mgr.AppendStatusMsg(ns, msg)
			return fmt.Errorf(msg)
		}
	}

	if len(conn.NodeInterfaceLabels) != 0 {

		if len(nodeMap) == 0 {
			msg := fmt.Sprintf("network service: %s, no interfaces specified to connect to node interface label",
				ns.Metadata.Name)
			mgr.AppendStatusMsg(ns, msg)
			return fmt.Errorf(msg)
		}

		if len(nodeMap) != 1 {
			msg := fmt.Sprintf("network service: %s, all interfaces must be on smae node to connect to node interface label",
				ns.Metadata.Name)
			mgr.AppendStatusMsg(ns, msg)
			return fmt.Errorf(msg)
		}

		nodeInterfaces, nodeIfTypes := ctlrPlugin.NetworkNodeMgr.FindInterfacesForThisLabelInNode(p2nArray[0].Node, conn.NodeInterfaceLabels)
		if len(nodeInterfaces) == 0 {
			msg := fmt.Sprintf("network service: %s, nodeLabels %v: must match at least one node interface: incorrect config",
				ns.Metadata.Name, conn.NodeInterfaceLabels)
			mgr.AppendStatusMsg(ns, msg)
			return fmt.Errorf(msg)
		}

		for _, nodeInterface := range nodeInterfaces {
			nodeInterface.Parent = p2nArray[0].Node
			var p2n controller.NetworkPodToNodeMap
			p2n.Node = p2nArray[0].Node
			p2n.Pod = p2nArray[0].Node
			p2nArray = append(p2nArray, p2n)
			netPodInterfaces = append(netPodInterfaces, nodeInterface)
		}
		for _, nodeIfType := range nodeIfTypes {
			networkPodTypes = append(networkPodTypes, nodeIfType)
		}
	}

	// see if the networkPods are on the same node ...
	if len(nodeMap) == 1 {
		return mgr.renderConnL3MPSameNode(ns, conn, connIndex, netPodInterfaces,
			nno, p2nArray, networkPodTypes)
	} else if staticNodesInInterfacesSpecified {
		msg := fmt.Sprintf("network service: %s, nodes %s/%s must be the same",
			ns.Metadata.Name,
			p2nArray[0].Node,
			p2nArray[1].Node)
		mgr.AppendStatusMsg(ns, msg)
		return fmt.Errorf(msg)
	}

	// now setup the connection between nodes
	return mgr.renderConnL3MPInterNode(ns, conn, connIndex, netPodInterfaces,
		nno, p2nArray, networkPodTypes, nodeMap)
}

// renderConnL3MPSameNode renders this L3MP connection set on same node
func (mgr *NetworkServiceMgr) renderConnL3MPSameNode(
	ns *controller.NetworkService,
	conn *controller.Connection,
	connIndex uint32,
	netPodInterfaces []*controller.Interface,
	nno *controller.NetworkNodeOverlay,
	p2nArray []controller.NetworkPodToNodeMap,
	networkPodTypes []string) error {

	// The interfaces should be created in the vnf and the vswitch then the vswitch
	// interfaces will be added associated with the vrf.

	//var l2bdIFs = make(map[string][]*l2.BridgeDomain_Interface, 0)

	nodeName := p2nArray[0].Node

	if conn.VrfId == 0 {
		conn.VrfId = ctlrPlugin.ramCache.VrfIDAllocator.Allocate()
	}

	vrfName := fmt.Sprintf("VRF_%d_%s_C%d", conn.VrfId, ns.Metadata.Name, connIndex+1)

	// if there is a conn/vrf loop address defined
	vrfLoopIfNameName := ""

	if conn.LoopbackAddress != "" {

		vrfLoopIfNameName = "IFLOOP_" + vrfName

		vppKV := vppagent.ConstructLoopbackInterface(
			nodeName,
			vrfLoopIfNameName,
			[]string{conn.LoopbackAddress},
			"",
			ctlrPlugin.SysParametersMgr.ResolveMtu(0),
			conn.VrfId,
			controller.IfAdminStatusEnabled,
			ctlrPlugin.SysParametersMgr.ResolveRxMode(""))
		RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
			ModelTypeNetworkService+"/"+ns.Metadata.Name,
			vppKV)
	}

	for i := 0; i < len(netPodInterfaces); i++ {
		log.Infof("Render connection interface pair: %+v - %+v", conn, netPodInterfaces[i])
		ifName, ifStatus, err := mgr.RenderConnInterfacePair(ns, nodeName, conn, netPodInterfaces[i], networkPodTypes[i], vrfLoopIfNameName)
		if err != nil {
			return err
		}

		//l2bdIF := &l2.BridgeDomain_Interface{
		//	Name:                    ifName,
		//	BridgedVirtualInterface: false,
		//}
		//l2bdIFs[nodeName] = append(l2bdIFs[nodeName], l2bdIF)

		if netPodInterfaces[i].IfType == controller.IfTypeMemif &&
			(netPodInterfaces[i].MemifParms != nil &&
				netPodInterfaces[i].MemifParms.Mode == controller.IfMemifModeIP) {

			if conn.GenerateStaticL3Routes {
				if len(ifStatus.IpAddresses) != 0 {
					desc := fmt.Sprintf("L3MP NS_%s_VRF_%d_CONN_%d", ns.Metadata.Name, conn.VrfId, connIndex+1)

					// on the vswitch ... route up to the pod
					l3sr := &controller.L3VRFRoute{
						Vpp: &controller.VPPRoute{
							VrfId:             conn.VrfId,
							Description:       desc,
							DstIpAddr:         ifStatus.IpAddresses[0],
							OutgoingInterface: ifName,
						},
					}
					vppKV := vppagent.ConstructStaticRoute(nodeName, l3sr)
					RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
						ModelTypeNetworkService+"/"+ns.Metadata.Name,
						vppKV)
				}
				if conn.LoopbackAddress != "" {

					// on the pod ... route down to the vswitch
					l3sr := &controller.L3VRFRoute{
						Vpp: &controller.VPPRoute{
							VrfId:             0,
							DstIpAddr:         conn.LoopbackAddress,
							OutgoingInterface: netPodInterfaces[i].Name,
						},
					}
					vppKV := vppagent.ConstructStaticRoute(p2nArray[i].Pod, l3sr)
					RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
						ModelTypeNetworkService+"/"+ns.Metadata.Name,
						vppKV)
				}
			}
		}

		if netPodInterfaces[i].IfType == controller.IfTypeMemif &&
			(netPodInterfaces[i].MemifParms == nil ||
				netPodInterfaces[i].MemifParms.Mode == controller.IfMemifModeEthernet) {

			if conn.GenerateStaticArps {

				// create an arp in the vswitch
				ae := &controller.L3ArpEntry{
					IpAddress:         ifStatus.IpAddresses[0],
					PhysAddress:       ifStatus.MacAddress,
					OutgoingInterface: ifName,
				}
				vppKV := vppagent.ConstructStaticArpEntry(nodeName, ae)
				RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
					ModelTypeNetworkService+"/"+ns.Metadata.Name,
					vppKV)

				// create an arp in the pod ... should check node type
				if networkPodTypes[i] == controller.NetworkPodTypeVPPContainer {
					ae = &controller.L3ArpEntry{
						IpAddress:         conn.LoopbackAddress,
						PhysAddress:       "FF:FF:FF:FF:FF:FF",
						OutgoingInterface: netPodInterfaces[i].Name,
					}
					vppKV = vppagent.ConstructStaticArpEntry(p2nArray[i].Pod, ae)
					RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
						ModelTypeNetworkService+"/"+ns.Metadata.Name,
						vppKV)
				}
			}
			if conn.GenerateStaticL3Routes {
				if len(ifStatus.IpAddresses) != 0 {
					desc := fmt.Sprintf("L3MP NS_%s_VRF_%d_CONN_%d", ns.Metadata.Name, conn.VrfId, connIndex+1)

					// on the vswitch ... route up to the pod
					l3sr := &controller.L3VRFRoute{
						Vpp: &controller.VPPRoute{
							VrfId:             conn.VrfId,
							Description:       desc,
							NextHopAddr:       vppagent.StripSlashAndSubnetIPAddress(ifStatus.IpAddresses[0]),
							DstIpAddr:         ifStatus.IpAddresses[0],
							OutgoingInterface: ifName,
						},
					}
					vppKV := vppagent.ConstructStaticRoute(nodeName, l3sr)
					RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
						ModelTypeNetworkService+"/"+ns.Metadata.Name,
						vppKV)
				}
				if conn.LoopbackAddress != "" {

					// on the pod ... route down to the vswitch
					l3sr := &controller.L3VRFRoute{
						Vpp: &controller.VPPRoute{
							VrfId:             0,
							DstIpAddr:         "0.0.0.0/0",
							NextHopAddr:       conn.LoopbackAddress,
							OutgoingInterface: netPodInterfaces[i].Name,
						},
					}
					vppKV := vppagent.ConstructStaticRoute(p2nArray[i].Pod, l3sr)
					RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
						ModelTypeNetworkService+"/"+ns.Metadata.Name,
						vppKV)
				}
			}
		}

		if netPodInterfaces[i].IfType == controller.IfTypeTap {

			if conn.GenerateStaticArps {

				// create an arp in the vswitch
				ae := &controller.L3ArpEntry{
					IpAddress:         ifStatus.IpAddresses[0],
					PhysAddress:       ifStatus.MacAddress,
					OutgoingInterface: ifName,
				}
				vppKV := vppagent.ConstructStaticArpEntry(nodeName, ae)
				RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
					ModelTypeNetworkService+"/"+ns.Metadata.Name,
					vppKV)

				// create an arp in the pod ... should check node type
				ae = &controller.L3ArpEntry{
					IpAddress:         conn.LoopbackAddress,
					PhysAddress:       "FF:FF:FF:FF:FF:FF",
					OutgoingInterface: netPodInterfaces[i].Name,
				}
				vppKV = vppagent.ConstructStaticArpLinuxEntry(p2nArray[i].Pod, ae)
				RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
					ModelTypeNetworkService+"/"+ns.Metadata.Name,
					vppKV)

			}
			if conn.GenerateStaticL3Routes {
				if len(ifStatus.IpAddresses) != 0 {
					desc := fmt.Sprintf("L3MP NS_%s_VRF_%d_CONN_%d", ns.Metadata.Name, conn.VrfId, connIndex+1)

					// on the vswitch ... route up to the pod
					l3sr := &controller.L3VRFRoute{
						Vpp: &controller.VPPRoute{
							VrfId:             conn.VrfId,
							Description:       desc,
							NextHopAddr:       vppagent.StripSlashAndSubnetIPAddress(ifStatus.IpAddresses[0]),
							DstIpAddr:         ifStatus.IpAddresses[0],
							OutgoingInterface: ifName,
						},
					}
					vppKV := vppagent.ConstructStaticRoute(nodeName, l3sr)
					RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
						ModelTypeNetworkService+"/"+ns.Metadata.Name,
						vppKV)
				}
				if conn.LoopbackAddress != "" {

					// on the pod ... route down to the vswitch
					l3sr := &controller.L3VRFRoute{
						Vpp: &controller.VPPRoute{
							VrfId:             0,
							DstIpAddr:         "0.0.0.0/0",
							NextHopAddr:       conn.LoopbackAddress,
							OutgoingInterface: netPodInterfaces[i].Name,
						},
					}
					vppKV := vppagent.ConstructStaticRoute(p2nArray[i].Pod, l3sr)
					RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
						ModelTypeNetworkService+"/"+ns.Metadata.Name,
						vppKV)
				}
			}
		}
	}

	// all VNFs are on the same node so no vxlan inter-node mesh code required but
	// the VNFs might be connected to an external node/router via hub and spoke

	if nno != nil {
		if nno.Spec.ServiceMeshType == controller.NetworkNodeOverlayTypeHubAndSpoke &&
			nno.Spec.ConnectionType == controller.NetworkNodeOverlayConnectionTypeVxlan {

			// construct a spoke set with this one one
			singleSpokeMap := make(map[string]bool)
			singleSpokeMap[nodeName] = true

			return ctlrPlugin.NetworkNodeOverlayMgr.renderConnL2MPVxlanHubAndSpoke(
				nno,
				ns,
				conn,
				connIndex,
				netPodInterfaces,
				p2nArray,
				networkPodTypes,
				singleSpokeMap,
				//l2bdIFs)
				nil)
		}
	}

	return nil //ns.RenderL2BD(conn, connIndex, nodeName, l2bdIFs[nodeName])
}

// renderConnL3MPInterNode renders this L3MP connection between nodes
func (mgr *NetworkServiceMgr) renderConnL3MPInterNode(
	ns *controller.NetworkService,
	conn *controller.Connection,
	connIndex uint32,
	netPodInterfaces []*controller.Interface,
	nno *controller.NetworkNodeOverlay,
	p2nArray []controller.NetworkPodToNodeMap,
	networkPodTypes []string,
	nodeMap map[string]bool) error {

	// The interfaces may be spread across a set of nodes (nodeMap), each of these
	// interfaces should be created in the pod and node's vswitch.  The other matter is the
	// inter-node connectivity.  Example: if vxlan mesh is the chosen inter node
	// strategy, for each node in the nodeMap, a vxlan tunnel mesh must be created
	// using a free vni from the mesh's vniPool.
	// And, each local i/f must have a vrf entry on every remote node.

	if conn.VrfId == 0 {
		//conn.VrfId = ctlrPlugin.ramCache.VrfIDAllocator.Allocate()
	}

	l2bdIFs := make(map[string][]*l2.BridgeDomain_Interface, 0)
	l3vrfs := make(map[string][]*controller.L3VRFRoute, 0)

	// create the interfaces from the pod to the vswitch, also construct l3vrf routes for the local
	// interfaces per node
	for i := 0; i < len(netPodInterfaces); i++ {

		ifName, ifStatus, err := mgr.RenderConnInterfacePair(ns, p2nArray[i].Node, conn, netPodInterfaces[i], networkPodTypes[i], "")
		if err != nil {
			return err
		}

		l2bdIF := &l2.BridgeDomain_Interface{
			Name:                    ifName,
			BridgedVirtualInterface: false,
			SplitHorizonGroup:       0,
		}
		l2bdIFs[p2nArray[i].Node] = append(l2bdIFs[p2nArray[i].Node], l2bdIF)

		if len(ifStatus.IpAddresses) != 0 {
			desc := fmt.Sprintf("L3MP NS_%s_VRF_%d_CONN_%d", ns.Metadata.Name, conn.VrfId, connIndex+1)
			l3sr := &controller.L3VRFRoute{
				Vpp: &controller.VPPRoute{
					VrfId:             conn.VrfId,
					Description:       desc,
					DstIpAddr:         vppagent.StripSlashAndSubnetIPAddress(ifStatus.IpAddresses[0]),
					OutgoingInterface: ifName,
				},
			}

			l3vrfs[p2nArray[i].Node] = append(l3vrfs[p2nArray[i].Node], l3sr)

			//vppKV := vppagent.ConstructStaticRoute(p2nArray[i].Node, l3sr)
			//RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
			//	ModelTypeNetworkService + "/" + ns.Metadata.Name,
			//	vppKV)
		}
		ae := &controller.L3ArpEntry{
			IpAddress:         vppagent.StripSlashAndSubnetIPAddress(ifStatus.IpAddresses[0]),
			PhysAddress:       ifStatus.MacAddress,
			OutgoingInterface: ifName,
		}
		vppKV := vppagent.ConstructStaticArpEntry(p2nArray[i].Node, ae)
		RenderTxnAddVppEntryToTxn(ns.Status.RenderedVppAgentEntries,
			ModelTypeNetworkService+"/"+ns.Metadata.Name,
			vppKV)
	}

	switch nno.Spec.ConnectionType {
	case controller.NetworkNodeOverlayConnectionTypeVxlan:
		switch nno.Spec.ServiceMeshType {
		case controller.NetworkNodeOverlayTypeMesh:
			return ctlrPlugin.NetworkNodeOverlayMgr.renderConnL3MPVxlanMesh(
				nno,
				ns,
				conn,
				connIndex,
				netPodInterfaces,
				p2nArray,
				networkPodTypes,
				nodeMap,
				l3vrfs,
				l2bdIFs)
		case controller.NetworkNodeOverlayTypeHubAndSpoke:
			return ctlrPlugin.NetworkNodeOverlayMgr.renderConnL2MPVxlanHubAndSpoke(
				nno,
				ns,
				conn,
				connIndex,
				netPodInterfaces,
				p2nArray,
				networkPodTypes,
				nodeMap,
				l2bdIFs)
		}
	default:
		msg := fmt.Sprintf("network-service: %s, conn: %d, node overlay: %s type not implemented",
			ns.Metadata.Name,
			connIndex+1,
			nno.Metadata.Name)
		mgr.AppendStatusMsg(ns, msg)
		return fmt.Errorf(msg)
	}

	return nil
}

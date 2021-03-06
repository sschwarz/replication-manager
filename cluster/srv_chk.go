// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"

	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/state"
)

/* Check replication health and return status string */
func (server *ServerMonitor) replicationCheck() string {
	if server.ClusterGroup.sme.IsInFailover() {
		return "In Failover"
	}
	if server.HaveWsrep {
		if server.IsWsrepSync {
			server.State = stateWsrep
			return "Galera OK"
		} else if server.IsWsrepDonor {
			server.State = stateWsrepDonor
			return "Galera OK"
		} else {
			server.State = stateWsrepLate
			return "Galera Late"
		}
	}
	if (server.State == stateSuspect || server.State == stateFailed) && server.IsSlave == false {
		return "Master OK"
	}

	if server.ClusterGroup.master != nil {
		if server.ServerID == server.ClusterGroup.master.ServerID {
			return "Master OK"
		}
	}
	if server.IsMaintenance {
		server.State = stateMaintenance
		return "Maintenance"
	}
	// when replication stopped Valid is null
	ss, err := server.GetSlaveStatus(server.ReplicationSourceName)
	if err != nil {
		return "Not a slave"
	}
	if ss.SecondsBehindMaster.Valid == false && server.ClusterGroup.sme.CanMonitor() {

		//	log.Printf("replicationCheck %s %s", server.SQLThread, server.IOThread)
		if ss.SlaveSQLRunning.String == "Yes" && ss.SlaveIORunning.String == "No" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlaveErr
			} else if server.IsRelay {
				server.State = stateRelayErr
			}
			return fmt.Sprintf("NOT OK, IO Stopped (%s)", ss.LastIOErrno.String)
		} else if ss.SlaveSQLRunning.String == "No" && ss.SlaveIORunning.String == "Yes" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlaveErr
			} else if server.IsRelay {
				server.State = stateRelayErr
			}
			return fmt.Sprintf("NOT OK, SQL Stopped (%s)", ss.LastSQLErrno.String)
		} else if ss.SlaveSQLRunning.String == "No" && ss.SlaveIORunning.String == "No" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlaveErr
			} else if server.IsRelay {
				server.State = stateRelayErr
			}
			return "NOT OK, ALL Stopped"
		} else if ss.SlaveSQLRunning.String == "Connecting" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlave
			} else if server.IsRelay {
				server.State = stateRelay
			}
			return "NOT OK, IO Connecting"
		}

		if server.IsRelay == false && server.IsMaxscale == false {
			server.State = stateSlave
		} else if server.IsRelay {
			server.State = stateRelay
		}
		return "Running OK"
	}

	if ss.SecondsBehindMaster.Int64 > 0 {
		if ss.SecondsBehindMaster.Int64 > server.ClusterGroup.conf.FailMaxDelay && server.ClusterGroup.conf.RplChecks == true {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlaveLate
			} else if server.IsRelay {
				server.State = stateRelayLate
			}

		} else {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlave
			} else if server.IsRelay {
				server.State = stateRelay
			}
		}
		return "Behind master"
	}
	if server.IsRelay == false && server.IsMaxscale == false {
		server.State = stateSlave
	} else if server.IsRelay {
		server.State = stateRelayLate
	}
	return "Running OK"
}

func (server *ServerMonitor) SlaveCheck() {
	sl := server
	if server.ClusterGroup.conf.ForceSlaveSemisync && sl.HaveSemiSync == false {
		server.ClusterGroup.LogPrintf("DEBUG", "Enforce semisync on slave %s", sl.URL)
		dbhelper.InstallSemiSync(sl.Conn)
	} else if sl.IsIgnored() == false && sl.HaveSemiSync == false {
		server.ClusterGroup.sme.AddState("WARN0048", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0048"], sl.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceBinlogRow && sl.HaveBinlogRow == false {
		// In non-multimaster mode, enforce read-only flag if the option is set
		dbhelper.SetBinlogFormat(sl.Conn, "ROW")
		server.ClusterGroup.LogPrintf("INFO", "Enforce binlog format ROW on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogRow == false && server.ClusterGroup.conf.AutorejoinFlashback == true {
		server.ClusterGroup.sme.AddState("WARN0049", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0049"], sl.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceSlaveReadOnly && sl.ReadOnly == "OFF" {
		// In non-multimaster mode, enforce read-only flag if the option is set
		sl.SetReadOnly()
		server.ClusterGroup.LogPrintf("INFO", "Enforce read only on slave %s", sl.URL)
	}
	if server.ClusterGroup.conf.ForceSlaveHeartbeat && sl.GetReplicationHearbeatPeriod() > 1 {
		dbhelper.SetSlaveHeartbeat(sl.Conn, "1")
		server.ClusterGroup.LogPrintf("INFO", "Enforce heartbeat to 1s on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.GetReplicationHearbeatPeriod() > 1 {
		server.ClusterGroup.sme.AddState("WARN0050", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0050"], sl.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceSlaveGtid && sl.GetReplicationUsingGtid() == "No" {
		dbhelper.SetSlaveGTIDMode(sl.Conn, "slave_pos")
		server.ClusterGroup.LogPrintf("INFO", "Enforce GTID replication on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.GetReplicationUsingGtid() == "No" {
		server.ClusterGroup.sme.AddState("WARN0051", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0051"], sl.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceSyncInnoDB && sl.HaveInnodbTrxCommit == false {
		dbhelper.SetSyncInnodb(sl.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce InnoDB durability on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveInnodbTrxCommit == false {
		server.ClusterGroup.sme.AddState("WARN0052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0052"], sl.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceBinlogChecksum && sl.HaveChecksum == false {
		dbhelper.SetBinlogChecksum(sl.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce checksum on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveChecksum == false {
		server.ClusterGroup.sme.AddState("WARN0053", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0053"], sl.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceBinlogSlowqueries && sl.HaveBinlogSlowqueries == false {
		dbhelper.SetBinlogSlowqueries(sl.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce log slow queries of replication on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogSlowqueries == false {
		server.ClusterGroup.sme.AddState("WARN0054", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0054"], sl.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceBinlogAnnotate && sl.HaveBinlogAnnotate == false {
		dbhelper.SetBinlogAnnotate(sl.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce annotate on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogAnnotate == false {
		server.ClusterGroup.sme.AddState("WARN0055", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0055"], sl.URL), ErrFrom: "TOPO"})
	}

	if server.ClusterGroup.conf.ForceBinlogCompress && sl.HaveBinlogCompress == false && sl.DBVersion.IsMariaDB() && sl.DBVersion.Major >= 10 && sl.DBVersion.Minor >= 2 {
		dbhelper.SetBinlogCompress(sl.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce binlog compression on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogCompress == false && sl.DBVersion.IsMariaDB() && sl.DBVersion.Major >= 10 && sl.DBVersion.Minor >= 2 {
		server.ClusterGroup.sme.AddState("WARN0056", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0056"], sl.URL), ErrFrom: "TOPO"})
	}
	if sl.IsIgnored() == false && sl.HaveLogSlaveUpdates == false {
		server.ClusterGroup.sme.AddState("WARN0057", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0057"], sl.URL), ErrFrom: "TOPO"})
	}
	if sl.IsIgnored() == false && sl.HaveGtidStrictMode == false {
		server.ClusterGroup.sme.AddState("WARN0058", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0058"], sl.URL), ErrFrom: "TOPO"})
	}

	/* DB read-only variable
	   if server.ClusterGroup.conf.ForceDiskRelayLogSizeLimit && sl.RelayLogSize != server.ClusterGroup.conf.ForceDiskRelayLogSizeLimitSize {
	     dbhelper.SetRelayLogSpaceLimit(sl.Conn, strconv.FormatUint(server.ClusterGroup.conf.ForceDiskRelayLogSizeLimitSize, 10))
	     server.ClusterGroup.LogPrintf("DEBUG: Enforce relay disk space limit on slave %s", sl.URL)
	   }*/
}

func (server *ServerMonitor) MasterCheck() {
	if server.ClusterGroup.conf.ForceSlaveSemisync && server.HaveSemiSync == false {
		server.ClusterGroup.LogPrintf("INFO", "Enforce semisync on Master %s", server.DSN)
		dbhelper.InstallSemiSync(server.Conn)
	} else if server.HaveSemiSync == false {
		server.ClusterGroup.sme.AddState("WARN0060", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0060"], server.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceBinlogRow && server.HaveBinlogRow == false {
		dbhelper.SetBinlogFormat(server.Conn, "ROW")
		server.ClusterGroup.LogPrintf("INFO", "Enforce binlog format ROW on Master %s", server.DSN)
	} else if server.HaveBinlogRow == false && server.ClusterGroup.conf.AutorejoinFlashback == true {
		server.ClusterGroup.sme.AddState("WARN0061", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0061"], server.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceSyncBinlog && server.HaveSyncBinLog == false {
		dbhelper.SetSyncBinlog(server.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce sync binlog on Master %s", server.DSN)
	} else if server.HaveSyncBinLog == false {
		server.ClusterGroup.sme.AddState("WARN0062", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0062"], server.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceSyncInnoDB && server.HaveSyncBinLog == false {
		dbhelper.SetSyncInnodb(server.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce innodb durability on Master %s", server.DSN)
	} else if server.HaveSyncBinLog == false {
		server.ClusterGroup.sme.AddState("WARN0064", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0064"], server.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceBinlogAnnotate && server.HaveBinlogAnnotate == false {
		dbhelper.SetBinlogAnnotate(server.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce binlog annotate on master %s", server.DSN)
	} else if server.HaveBinlogAnnotate == false {
		server.ClusterGroup.sme.AddState("WARN0067", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0067"], server.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceBinlogChecksum && server.HaveChecksum == false {
		dbhelper.SetBinlogChecksum(server.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce ckecksum annotate on master %s", server.DSN)
	} else if server.HaveChecksum == false {
		server.ClusterGroup.sme.AddState("WARN0065", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0065"], server.URL), ErrFrom: "TOPO"})
	}
	if server.ClusterGroup.conf.ForceBinlogCompress && server.HaveBinlogCompress == false && server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 && server.DBVersion.Minor >= 2 {
		dbhelper.SetBinlogCompress(server.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce binlog compression on master %s", server.DSN)
	} else if server.HaveBinlogCompress == false && server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 && server.DBVersion.Minor >= 2 {
		server.ClusterGroup.sme.AddState("WARN0068", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0068"], server.URL), ErrFrom: "TOPO"})
	}
	if server.HaveLogSlaveUpdates == false {
		server.ClusterGroup.sme.AddState("WARN0069", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0069"], server.URL), ErrFrom: "TOPO"})
	}
	if server.HaveGtidStrictMode == false {
		server.ClusterGroup.sme.AddState("WARN0070", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0070"], server.URL), ErrFrom: "TOPO"})
	}
}

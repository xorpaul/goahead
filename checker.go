package main

import (
	"fmt"
	"strings"
	"time"
)

func checkForRebootedSystems() {

	Debugf("Starting checker")

	for {
		cc := <-checkCluster
		go func() {
			rid := cc.Response.RequestID
			Debugf(rid + " Starting check for rebooted system in cluster " + cc.Response.FoundCluster + " with fqdn: " + cc.Response.RequestingFqdn)

			Debugf(rid + " Sleeping for reboot_completion_check_offset: " + cc.ClusterSetting.RebootCompletionCheckOffset.String())
			time.Sleep(cc.ClusterSetting.RebootCompletionCheckOffset)

			for {
				command := strings.Replace(cc.ClusterSetting.RebootCompletionCheck, "{:%fqdn%:}", cc.Response.RequestingFqdn, -1)
				er := executeCommand(command, 5, true)
				fmt.Println(er)

				if er.returnCode == 0 {
					break
				}
				Debugf(rid + " Sleeping for reboot_completion_check_interval: " + cc.ClusterSetting.RebootCompletionCheckInterval.String())
				time.Sleep(cc.ClusterSetting.RebootCompletionCheckInterval)
			}
			Debugf(rid + "fqdn: " + cc.Response.RequestingFqdn + " seems to have successfully rebooted in cluster " + cc.Response.FoundCluster)
			// decrement current restarts for cluster
		}()
	}

}

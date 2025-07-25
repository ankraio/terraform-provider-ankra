

package provider

		return nil
	}
	client := &http.Client{}
	urlStr := fmt.Sprintf("https://platform.ankra.app/api/v1/clusters/%s", clusterName)
	log.Printf("[DEBUG] Delete request URL: %s", urlStr)
	req, err := http.NewRequest("DELETE", urlStr, nil)
	if err != nil {
		return diag.FromErr(err)
	}
	req.Header.Set("Authorization", "Bearer "+ankraToken)
	req.Header.Set("Content-Type", "application/json")
	log.Printf("[DEBUG] Delete request headers: %v", req.Header)
	resp, err := client.Do(req)
	if err != nil {
		return diag.FromErr(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	log.Printf("[DEBUG] Delete response status: %s", resp.Status)
	log.Printf("[DEBUG] Delete response body: %s", buf.String())
	if resp.StatusCode == 200 || resp.StatusCode == 204 || resp.StatusCode == 404 {
		return nil
	}
	return diag.Errorf("Failed to delete cluster by name: %s. Response body: %s", resp.Status, buf.String())
}


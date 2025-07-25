package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceAnkraCluster() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceAnkraClusterCreate,
		ReadContext:   resourceAnkraClusterRead,
		UpdateContext: resourceAnkraClusterUpdate,
		DeleteContext: resourceAnkraClusterDelete,
		Schema: map[string]*schema.Schema{
			"environment":            {Type: schema.TypeString, Required: true, ForceNew: true},
			"github_credential_name": {Type: schema.TypeString, Required: true, ForceNew: false},
			"github_branch":          {Type: schema.TypeString, Required: true, ForceNew: false},
			"github_repository":      {Type: schema.TypeString, Required: true, ForceNew: false},
			"ankra_token":            {Type: schema.TypeString, Required: true, Sensitive: true, ForceNew: true},
			"cluster_id":             {Type: schema.TypeString, Computed: true},
		},
	}
}

func resourceAnkraClusterCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	payload := map[string]interface{}{
		"name":        d.Get("environment").(string),
		"description": "Managed by Terraform",
		"spec": map[string]interface{}{
			"git_repository": map[string]interface{}{
				"provider":        "github",
				"credential_name": d.Get("github_credential_name").(string),
				"branch":          d.Get("github_branch").(string),
				"repository":      d.Get("github_repository").(string),
			},
			"stacks": []interface{}{},
		},
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return diag.FromErr(err)
	}
	req, err := http.NewRequest("POST", "https://platform.ankra.app/api/v1/clusters/import", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return diag.FromErr(err)
	}
	req.Header.Set("Authorization", "Bearer "+d.Get("ankra_token").(string))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return diag.FromErr(err)
	}
	defer resp.Body.Close()
	var respData struct {
		ClusterID string `json:"cluster_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return diag.FromErr(err)
	}
	if respData.ClusterID == "" {
		return diag.Errorf("Failed to create cluster: missing cluster_id")
	}
	d.SetId(respData.ClusterID)
	d.Set("cluster_id", respData.ClusterID)
	return nil
}

func resourceAnkraClusterRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	// No-op: Do not unset the ID, so Terraform always attempts destroy
	return nil
}

func resourceAnkraClusterUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	ankraToken := d.Get("ankra_token").(string)
	payload := map[string]interface{}{
		"name":        d.Get("environment").(string),
		"description": "Managed by Terraform",
		"spec": map[string]interface{}{
			"git_repository": map[string]interface{}{
				"provider":        "github",
				"credential_name": d.Get("github_credential_name").(string),
				"branch":          d.Get("github_branch").(string),
				"repository":      d.Get("github_repository").(string),
			},
			"stacks": []interface{}{},
		},
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return diag.FromErr(err)
	}
	req, err := http.NewRequest("POST", "https://platform.ankra.app/api/v1/clusters/import", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return diag.FromErr(err)
	}
	req.Header.Set("Authorization", "Bearer "+ankraToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return diag.FromErr(err)
	}
	defer resp.Body.Close()
	var respData struct {
		ClusterID string `json:"cluster_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return diag.FromErr(err)
	}
	if respData.ClusterID == "" {
		return diag.Errorf("Failed to update cluster: missing cluster_id")
	}
	d.SetId(respData.ClusterID)
	d.Set("cluster_id", respData.ClusterID)
	return nil
}

func resourceAnkraClusterDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	ankraToken := d.Get("ankra_token").(string)
	clusterName := strings.TrimSpace(d.Get("environment").(string))
	if clusterName == "" {
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

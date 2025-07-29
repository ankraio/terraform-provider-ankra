package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func dataSourceAnkraClusters() *schema.Resource {
	return &schema.Resource{
		ReadContext: dataSourceAnkraClustersRead,
		Schema: map[string]*schema.Schema{
			"ankra_token": {
				Type:     schema.TypeString,
				Required: true,
				Sensitive: true,
			},
			"clusters": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"id":   {Type: schema.TypeString, Computed: true},
						"name": {Type: schema.TypeString, Computed: true},
					},
				},
			},
		},
	}
}

func dataSourceAnkraClustersRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	   ankraTokenVal, ok := d.GetOk("ankra_token")
	   if !ok {
			   // Try environment variable or fail
			   return diag.Errorf("ankra_token must be set for data source")
	   }
	   ankraToken, ok := ankraTokenVal.(string)
	   if !ok {
			   return diag.Errorf("ankra_token must be a string")
	   }
	   client := &http.Client{}
	   req, err := http.NewRequest("GET", "https://platform.ankra.app/api/v1/clusters", nil)
	   if err != nil {
			   return diag.FromErr(err)
	   }
	   req.Header.Set("Authorization", "Bearer "+ankraToken)
	   req.Header.Set("Content-Type", "application/json")
	   resp, err := client.Do(req)
	   if err != nil {
			   return diag.FromErr(err)
	   }
	   defer resp.Body.Close()
	   if resp.StatusCode != 200 {
			   return diag.Errorf("Failed to list clusters: %s", resp.Status)
	   }
	   var listResp struct {
			   Clusters []struct {
					   ID   string `json:"id"`
					   Name string `json:"name"`
			   } `json:"clusters"`
	   }
	   if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
			   return diag.FromErr(err)
	   }
	   clusters := make([]map[string]interface{}, 0, len(listResp.Clusters))
	   for _, c := range listResp.Clusters {
			   clusters = append(clusters, map[string]interface{}{
					   "id":   c.ID,
					   "name": c.Name,
			   })
	   }
	   if err := d.Set("clusters", clusters); err != nil {
			   return diag.FromErr(err)
	   }
	   d.SetId("ankra_clusters")
	   return nil
}

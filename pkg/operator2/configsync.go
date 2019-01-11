package operator2

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

func (c *osinOperator) handleConfigSync(_ *configv1.OAuth) ([]idpSyncData, error) {
	// pretend we have this one instead of the real input for now
	// this has the bits we need to care about
	config := &configv1.OAuth{
		Spec: configv1.OAuthSpec{
			IdentityProviders: []configv1.IdentityProvider{
				{
					Name: "happy",
					ProviderConfig: configv1.IdentityProviderConfig{
						Type: configv1.IdentityProviderTypeHTPasswd,
						HTPasswd: &configv1.HTPasswdIdentityProvider{
							FileData: configv1.SecretNameReference{
								Name: "fancy",
							},
						},
					},
				},
				{
					Name: "new",
					ProviderConfig: configv1.IdentityProviderConfig{
						Type: configv1.IdentityProviderTypeOpenID,
						OpenID: &configv1.OpenIDIdentityProvider{
							ClientSecret: configv1.SecretNameReference{
								Name: "fancy",
							},
							CA: configv1.ConfigMapNameReference{
								Name: "mah-ca",
							},
						},
					},
				},
			},
			Templates: configv1.OAuthTemplates{}, // TODO fix
		},
	}

	// TODO we probably need listers
	configMapClient := c.configMaps.ConfigMaps(userConfigNamespace)
	secretClient := c.secrets.Secrets(userConfigNamespace)

	configMaps, err := configMapClient.List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	secrets, err := secretClient.List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	prefixConfigMapNames := sets.NewString()
	prefixSecretNames := sets.NewString()

	// TODO this has too much boilerplate

	for _, cm := range configMaps.Items {
		if strings.HasPrefix(cm.Name, userConfigPrefix) {
			prefixConfigMapNames.Insert(cm.Name)
		}
	}

	for _, secret := range secrets.Items {
		if strings.HasPrefix(secret.Name, userConfigPrefix) {
			prefixSecretNames.Insert(secret.Name)
		}
	}

	inUseConfigMapNames := sets.NewString()
	inUseSecretNames := sets.NewString()

	data := convertToData(config.Spec.IdentityProviders)
	for _, d := range data {
		for dest, src := range d.configMaps {
			syncOrDie(c.resourceSyncer.SyncConfigMap, dest, src.src)
			inUseConfigMapNames.Insert(dest)
		}
		for dest, src := range d.secrets {
			syncOrDie(c.resourceSyncer.SyncSecret, dest, src.src)
			inUseSecretNames.Insert(dest)
		}
	}

	notInUseConfigMapNames := prefixConfigMapNames.Difference(inUseConfigMapNames)
	notInUseSecretNames := prefixSecretNames.Difference(inUseSecretNames)

	// TODO maybe update resource syncer in lib-go to cleanup its map as needed
	// it does not really matter, we are talking as worse case of
	// a few unneeded strings and a few unnecessary deletes
	for dest := range notInUseConfigMapNames {
		syncOrDie(c.resourceSyncer.SyncConfigMap, dest, "")
	}
	for dest := range notInUseSecretNames {
		syncOrDie(c.resourceSyncer.SyncSecret, dest, "")
	}

	return data, nil
}

type idpSyncData struct {
	// both maps are dest -> source
	// dest is metadata.name for resource in our deployment's namespace
	configMaps map[string]sourceData
	secrets    map[string]sourceData
}

type sourceData struct {
	src    string
	volume corev1.Volume
	mount  corev1.VolumeMount
}

func convertToData(idps []configv1.IdentityProvider) []idpSyncData {
	out := make([]idpSyncData, 0, len(idps))
	for i, idp := range idps {
		pc := idp.ProviderConfig
		switch pc.Type {
		case configv1.IdentityProviderTypeHTPasswd:
			p := pc.HTPasswd // TODO could panic if invalid (applies to all IDPs)

			fileData := p.FileData.Name
			dest := getName(i, fileData, configv1.HTPasswdDataKey)
			volume, mount := secretVolume(i, dest, configv1.HTPasswdDataKey)

			out = append(out,
				idpSyncData{
					secrets: map[string]sourceData{
						dest: {
							src:    fileData,
							volume: volume,
							mount:  mount,
						},
					},
				},
			)
		case configv1.IdentityProviderTypeOpenID:
			p := pc.OpenID

			ca := p.CA.Name
			caDest := getName(i, ca, corev1.ServiceAccountRootCAKey)
			caVolume, caMount := configMapVolume(i, caDest, corev1.ServiceAccountRootCAKey)

			clientSecret := p.ClientSecret.Name
			clientSecretDest := getName(i, clientSecret, configv1.ClientSecretKey)
			clientSecretVolume, clientSecretMount := secretVolume(i, clientSecretDest, configv1.ClientSecretKey)

			out = append(out,
				idpSyncData{
					configMaps: map[string]sourceData{
						caDest: {
							src:    ca,
							volume: caVolume,
							mount:  caMount,
						},
					},
					secrets: map[string]sourceData{
						clientSecretDest: {
							src:    clientSecret,
							volume: clientSecretVolume,
							mount:  clientSecretMount,
						},
					},
				},
			)
		default:
			panic("TODO")
		}
	}
	return out
}

const userConfigPrefix = "v4.0-config-user-idp-"

func getName(i int, name, key string) string {
	// TODO this scheme relies on each IDP struct not using the same key for more than one field
	// I think we can do better, but here is a start
	// A generic function that uses reflection may work too
	// granted the key bit can be easily solved by the caller adding a postfix to the key if it is reused
	return fmt.Sprintf("%s%d-%s-%s", userConfigPrefix, i, name, key)
}

const userConfigPathPrefix = "/var/config/user/idp/"

func getPath(i int, resource, name string) string {
	return fmt.Sprintf("%s%d/%s/%s", userConfigPathPrefix, i, resource, name)
}

func syncOrDie(syncFunc func(dest, src resourcesynccontroller.ResourceLocation) error, dest, src string) {
	ns := userConfigNamespace
	if len(src) == 0 { // handle delete
		ns = ""
	}
	if err := syncFunc(
		resourcesynccontroller.ResourceLocation{
			Namespace: targetName, // TODO fix
			Name:      dest,
		},
		resourcesynccontroller.ResourceLocation{
			Namespace: ns,
			Name:      src,
		},
	); err != nil {
		panic(err) // implies incorrect informer wiring, we can never recover from this, just die
	}
}

func secretVolume(i int, name, key string) (corev1.Volume, corev1.VolumeMount) {
	volume := corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: name,
				Items: []corev1.KeyToPath{
					{
						Key:  key,
						Path: key,
					},
				},
			},
		},
	}
	mount := corev1.VolumeMount{
		Name:      name,
		ReadOnly:  true,
		MountPath: getPath(i, "secret", name),
	}
	return volume, mount
}

func configMapVolume(i int, name, key string) (corev1.Volume, corev1.VolumeMount) {
	volume := corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
				Items: []corev1.KeyToPath{
					{
						Key:  key,
						Path: key,
					},
				},
			},
		},
	}
	mount := corev1.VolumeMount{
		Name:      name,
		ReadOnly:  true,
		MountPath: getPath(i, "configmap", name),
	}
	return volume, mount
}

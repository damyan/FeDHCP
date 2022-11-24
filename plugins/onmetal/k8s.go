package onmetal

import (
	"context"
	"net"
	"reflect"
	"strings"

	ipamv1alpha1 "github.com/onmetal/ipam/api/v1alpha1"
	inventoryv1alpha1 "github.com/onmetal/metal-api/apis/inventory/v1alpha1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	origin = "dhcp"
)

type K8sClient struct {
	Client    client.Client
	Namespace string
	Subnet    string
}

func NewK8sClient(namespace string, subnet string) K8sClient {
	if err := inventoryv1alpha1.AddToScheme(scheme.Scheme); err != nil {
		log.Fatal("Unable to add registered types inventory to client scheme: ", err)
	}

	if err := ipamv1alpha1.AddToScheme(scheme.Scheme); err != nil {
		log.Fatal("Unable to add registered types inventory to client scheme: ", err)
	}

	cl, err := client.New(config.GetConfigOrDie(), client.Options{})
	if err != nil {
		log.Fatal("Failed to create a client: ", err)
	}
	return K8sClient{
		Client:    cl,
		Namespace: namespace,
		Subnet:    subnet,
	}
}

func (k K8sClient) createIpamIP(ipaddr net.IP, mac net.HardwareAddr) error {
	ctx := context.Background()
	macKey := strings.ReplaceAll(mac.String(), ":", "")
	name := macKey + "-" + origin

	ip, err := ipamv1alpha1.IPAddrFromString(ipaddr.String())
	if err != nil {
		err = errors.Wrapf(err, "Failed to parse IP %s", ip)
		return err
	}

	ipamIP := &ipamv1alpha1.IP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: k.Namespace,
			Labels: map[string]string{
				"ip":     strings.ReplaceAll(ipaddr.String(), ":", "_"),
				"mac":    macKey,
				"origin": origin,
			},
		},
		Spec: ipamv1alpha1.IPSpec{
			IP: ip,
			Subnet: corev1.LocalObjectReference{
				Name: k.Subnet,
			},
		},
	}

	existingIpamIP := ipamIP.DeepCopy()
	err = k.Client.Get(ctx, client.ObjectKeyFromObject(ipamIP), existingIpamIP)
	if err != nil && !apierrors.IsNotFound(err) {
		err = errors.Wrapf(err, "Failed to get IP %s in namespace %s", ipamIP.Name, ipamIP.Namespace)
		return err
	}

	createIpamIP := false
	// create IPAM IP if not exists or delete existing if ip differs
	if apierrors.IsNotFound(err) {
		createIpamIP = true
	} else {
		if !reflect.DeepEqual(ipamIP.Spec, existingIpamIP.Spec) {
			log.Infof("Delete old IP %s in namespace %s", existingIpamIP.Name, existingIpamIP.Namespace)

			// delete old IP object
			err = k.Client.Delete(ctx, existingIpamIP)
			if err != nil {
				err = errors.Wrapf(err, "Failed to delete IP %s in namespace %s", existingIpamIP.Name, existingIpamIP.Namespace)
				return err
			}

			createIpamIP = true
		}
	}

	if createIpamIP {
		err = k.Client.Create(ctx, ipamIP)
		if err != nil {
			err = errors.Wrapf(err, "Failed to create IP %s in namespace %s", ipamIP.Name, ipamIP.Namespace)
			return err
		}
	}

	return nil
}

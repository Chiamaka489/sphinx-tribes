package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi"
	"github.com/rs/xid"
	"github.com/stakwork/sphinx-tribes/auth"
	"github.com/stakwork/sphinx-tribes/db"
	"github.com/stakwork/sphinx-tribes/utils"
	"gorm.io/gorm"
)

type workspaceHandler struct {
	db                       db.Database
	generateBountyHandler    func(bounties []db.Bounty) []db.BountyResponse
	getLightningInvoice      func(payment_request string) (db.InvoiceResult, db.InvoiceError)
	userHasAccess            func(pubKeyFromAuth string, uuid string, role string) bool
	userHasManageBountyRoles func(pubKeyFromAuth string, uuid string) bool
}

func NewWorkspaceHandler(database db.Database) *workspaceHandler {
	bHandler := NewBountyHandler(http.DefaultClient, database)
	dbConf := db.NewDatabaseConfig(&gorm.DB{})
	return &workspaceHandler{
		db:                       database,
		generateBountyHandler:    bHandler.GenerateBountyResponse,
		getLightningInvoice:      bHandler.GetLightningInvoice,
		userHasAccess:            dbConf.UserHasAccess,
		userHasManageBountyRoles: dbConf.UserHasManageBountyRoles,
	}
}

func (oh *workspaceHandler) CreateOrEditWorkspace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	now := time.Now()

	workspace := db.Organization{}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	err := json.Unmarshal(body, &workspace)

	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	workspace.Name = strings.TrimSpace(workspace.Name)

	if len(workspace.Name) == 0 || len(workspace.Name) > 20 {
		fmt.Printf("invalid organization name %s\n", workspace.Name)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode("Error: organization name must be present and should not exceed 20 character")
		return
	}

	if len(workspace.Description) > 120 {
		fmt.Printf("invalid organization name %s\n", workspace.Description)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode("Error: organization description should not exceed 120 character")
		return
	}

	if pubKeyFromAuth != workspace.OwnerPubKey {
		hasRole := db.UserHasAccess(pubKeyFromAuth, workspace.Uuid, db.EditOrg)
		if !hasRole {
			fmt.Println(pubKeyFromAuth)
			fmt.Println(workspace.OwnerPubKey)
			fmt.Println("mismatched pubkey")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode("Don't have access to Edit workspace")
			return
		}
	}

	// Validate struct data
	err = db.Validate.Struct(workspace)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("Error: did not pass validation test : %s", err)
		json.NewEncoder(w).Encode(msg)
		return
	}

	if workspace.Github != "" && !strings.Contains(workspace.Github, "github.com/") {
		w.WriteHeader(http.StatusBadRequest)
		msg := "Error: not a valid github"
		json.NewEncoder(w).Encode(msg)
		return
	}

	existing := oh.db.GetWorkspaceByUuid(workspace.Uuid)
	if existing.ID == 0 { // new!
		if workspace.ID != 0 { // can't try to "edit" if it does not exist already
			fmt.Println("cant edit non existing")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		name := workspace.Name

		// check if the organization name already exists
		workspace_same_name := oh.db.GetWorkspaceByName(name)

		if workspace_same_name.Name == name && workspace_same_name.Uuid != workspace.Uuid {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode("Workspace name already exists - " + name + " " + workspace.Uuid + " | " + workspace_same_name.Uuid)
			return
		} else {
			workspace.Created = &now
			workspace.Updated = &now
			if len(workspace.Uuid) == 0 {
				workspace.Uuid = xid.New().String()
			}
			workspace.Name = name
		}
	} else {
		// if workspace.ID == 0 {
		// 	// can't create that already exists
		// 	fmt.Println("can't create existing organization")
		// 	w.WriteHeader(http.StatusUnauthorized)
		// 	return
		// }

		// if workspace.ID != existing.ID { // can't edit someone else's
		// 	fmt.Println("cant edit another organization")
		// 	w.WriteHeader(http.StatusUnauthorized)
		// 	return
		// }
	}

	p, err := oh.db.CreateOrEditWorkspace(workspace)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(p)
}

func GetWorkspaces(w http.ResponseWriter, r *http.Request) {
	orgs := db.DB.GetWorkspaces(r)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(orgs)
}

func GetWorkspacesCount(w http.ResponseWriter, r *http.Request) {
	count := db.DB.GetWorkspacesCount()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(count)
}

func GetWorkspaceByUuid(w http.ResponseWriter, r *http.Request) {
	uuid := chi.URLParam(r, "uuid")
	workspace := db.DB.GetWorkspaceByUuid(uuid)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(workspace)
}

func CreateWorkspaceUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	now := time.Now()

	orgUser := db.OrganizationUsers{}
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	err = json.Unmarshal(body, &orgUser)

	// get orgnanization
	workspace := db.DB.GetWorkspaceByUuid(orgUser.OrgUuid)

	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// check if the user is the organization admin
	if orgUser.OwnerPubKey == workspace.OwnerPubKey {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("Cannot add organization admin as a user")
		return
	}

	// check if the user tries to add their self
	if pubKeyFromAuth == orgUser.OwnerPubKey {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("Cannot add userself as a user")
		return
	}

	// if not the orgnization admin
	hasRole := db.UserHasAccess(pubKeyFromAuth, orgUser.OrgUuid, db.AddUser)
	if !hasRole {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("Don't have access to add user")
		return
	}

	// check if the user exists on peoples table
	isUser := db.DB.GetPersonByPubkey(orgUser.OwnerPubKey)
	if isUser.OwnerPubKey != orgUser.OwnerPubKey {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("User doesn't exists in people")
		return
	}

	// check if user already exists
	userExists := db.DB.GetWorkspaceUser(orgUser.OwnerPubKey, orgUser.OrgUuid)

	if userExists.ID != 0 {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("User already exists")
		return
	}

	orgUser.Created = &now
	orgUser.Updated = &now

	// create user
	user := db.DB.CreateWorkspaceUser(orgUser)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(user)
}

func GetWorkspaceUsers(w http.ResponseWriter, r *http.Request) {
	uuid := chi.URLParam(r, "uuid")
	orgUsers, _ := db.DB.GetWorkspaceUsers(uuid)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(orgUsers)
}

func GetWorkspaceUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)

	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	uuid := chi.URLParam(r, "uuid")
	orgUser := db.DB.GetWorkspaceUser(pubKeyFromAuth, uuid)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(orgUser)
}

func GetWorkspaceUsersCount(w http.ResponseWriter, r *http.Request) {
	uuid := chi.URLParam(r, "uuid")
	count := db.DB.GetWorkspaceUsersCount(uuid)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(count)
}

func DeleteWorkspaceUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)

	orgUser := db.OrganizationUsersData{}
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	err = json.Unmarshal(body, &orgUser)

	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	workspace := db.DB.GetWorkspaceByUuid(orgUser.OrgUuid)

	if orgUser.OwnerPubKey == workspace.OwnerPubKey {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("Cannot delete organization admin")
		return
	}

	hasRole := db.UserHasAccess(pubKeyFromAuth, orgUser.OrgUuid, db.DeleteUser)
	if !hasRole {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("Don't have access to delete user")
		return
	}

	db.DB.DeleteWorkspaceUser(orgUser, orgUser.OrgUuid)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(orgUser)
}

func GetBountyRoles(w http.ResponseWriter, r *http.Request) {
	roles := db.DB.GetBountyRoles()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(roles)
}

func AddUserRoles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	uuid := chi.URLParam(r, "uuid")
	user := chi.URLParam(r, "user")
	now := time.Now()

	if uuid == "" || user == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("no uuid, or user pubkey")
		return
	}

	roles := []db.UserRoles{}
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	err = json.Unmarshal(body, &roles)

	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	if pubKeyFromAuth == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("no pubkey from auth")
		return
	}

	// if not the orgnization admin
	hasRole := db.UserHasAccess(pubKeyFromAuth, uuid, db.AddRoles)
	isUser := db.CheckUser(roles, pubKeyFromAuth)

	if isUser {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("cannot add roles for self")
		return
	}

	// check if the user added his pubkey to the route
	if pubKeyFromAuth == user {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("auth pubkey cannot be the same with user's")
		return
	}

	if !hasRole {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("user does not have adequate permissions to add roles")
		return
	}

	rolesMap := db.GetRolesMap()
	insertRoles := []db.UserRoles{}
	for _, role := range roles {
		_, ok := rolesMap[role.Role]
		// if any of the roles does not exists return an error
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode("not a valid user role")
			return
		}

		// check if the user has the role he his trying to add to another user
		okUser := db.UserHasAccess(pubKeyFromAuth, uuid, role.Role)
		// if the user does not have any of the roles he wants to add return an error
		if !okUser {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode("cannot add a role you don't have")
			return
		}

		// add created time for insert
		role.Created = &now
		insertRoles = append(insertRoles, role)
	}

	// check if user already exists
	userExists := db.DB.GetWorkspaceUser(user, uuid)

	// if not the organization admin
	if userExists.OwnerPubKey != user || userExists.OrgUuid != uuid {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("User does not exists in the organization")
		return
	}

	db.DB.CreateUserRoles(insertRoles, uuid, user)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(insertRoles)
}

func GetUserRoles(w http.ResponseWriter, r *http.Request) {
	uuid := chi.URLParam(r, "uuid")
	user := chi.URLParam(r, "user")

	userRoles := db.DB.GetUserRoles(uuid, user)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(userRoles)
}

func GetUserWorkspaces(w http.ResponseWriter, r *http.Request) {
	userIdParam := chi.URLParam(r, "userId")
	userId, _ := utils.ConvertStringToUint(userIdParam)

	if userId == 0 {
		fmt.Println("provide user id")
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	user := db.DB.GetPerson(userId)

	// get the workspaces created by the user, then get all the workspaces
	// the user has been added to, loop through to get the organization
	workspaces := GetCreatedWorkspaces(user.OwnerPubKey)

	assignedWorkspaces := db.DB.GetUserAssignedWorkspaces(user.OwnerPubKey)
	for _, value := range assignedWorkspaces {
		uuid := value.OrgUuid
		organization := db.DB.GetWorkspaceByUuid(uuid)
		bountyCount := db.DB.GetWorkspaceBountyCount(uuid)
		hasRole := db.UserHasAccess(user.OwnerPubKey, uuid, db.ViewReport)

		// don't add deleted workspaces to the list
		if !organization.Deleted {
			if hasRole {
				budget := db.DB.GetWorkspaceBudget(uuid)
				organization.Budget = budget.TotalBudget
			} else {
				organization.Budget = 0
			}
			organization.BountyCount = bountyCount

			workspaces = append(workspaces, organization)
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(workspaces)
}

func (oh *workspaceHandler) GetUserDropdownWorkspaces(w http.ResponseWriter, r *http.Request) {
	userIdParam := chi.URLParam(r, "userId")
	userId, _ := utils.ConvertStringToUint(userIdParam)

	if userId == 0 {
		fmt.Println("provide user id")
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	user := db.DB.GetPerson(userId)

	// get the workspaces created by the user, then get all the workspaces
	// the user has been added to, loop through to get the organization
	workspaces := GetCreatedWorkspaces(user.OwnerPubKey)

	assignedWorkspaces := db.DB.GetUserAssignedWorkspaces(user.OwnerPubKey)
	for _, value := range assignedWorkspaces {
		uuid := value.OrgUuid
		organization := db.DB.GetWorkspaceByUuid(uuid)
		bountyCount := db.DB.GetWorkspaceBountyCount(uuid)
		hasRole := db.UserHasAccess(user.OwnerPubKey, uuid, db.ViewReport)
		hasBountyRoles := oh.userHasManageBountyRoles(user.OwnerPubKey, uuid)

		// don't add deleted workspaces to the list
		if !organization.Deleted && hasBountyRoles {
			if hasRole {
				budget := db.DB.GetWorkspaceBudget(uuid)
				organization.Budget = budget.TotalBudget
			} else {
				organization.Budget = 0
			}
			organization.BountyCount = bountyCount

			workspaces = append(workspaces, organization)
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(workspaces)
}

func GetCreatedWorkspaces(pubkey string) []db.Organization {
	workspaces := db.DB.GetUserCreatedWorkspaces(pubkey)
	// add bounty count to the organization
	for index, value := range workspaces {
		uuid := value.Uuid
		bountyCount := db.DB.GetWorkspaceBountyCount(uuid)
		hasRole := db.UserHasAccess(pubkey, uuid, db.ViewReport)

		if hasRole {
			budget := db.DB.GetWorkspaceBudget(uuid)
			workspaces[index].Budget = budget.TotalBudget
		} else {
			workspaces[index].Budget = 0
		}
		workspaces[index].BountyCount = bountyCount
	}
	return workspaces
}

func (oh *workspaceHandler) GetWorkspaceBounties(w http.ResponseWriter, r *http.Request) {
	uuid := chi.URLParam(r, "uuid")

	// get the organization bounties
	organizationBounties := oh.db.GetWorkspaceBounties(r, uuid)

	var bountyResponse []db.BountyResponse = oh.generateBountyHandler(organizationBounties)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(bountyResponse)
}

func (oh *workspaceHandler) GetWorkspaceBountiesCount(w http.ResponseWriter, r *http.Request) {
	uuid := chi.URLParam(r, "uuid")

	organizationBountiesCount := oh.db.GetWorkspaceBountiesCount(r, uuid)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(organizationBountiesCount)
}

func (oh *workspaceHandler) GetWorkspaceBudget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	uuid := chi.URLParam(r, "uuid")

	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// if not the organization admin
	hasRole := oh.userHasAccess(pubKeyFromAuth, uuid, db.ViewReport)
	if !hasRole {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("Don't have access to view budget")
		return
	}

	// get the organization budget
	organizationBudget := oh.db.GetWorkspaceStatusBudget(uuid)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(organizationBudget)
}

func (oh *workspaceHandler) GetWorkspaceBudgetHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	uuid := chi.URLParam(r, "uuid")

	// if not the organization admin
	hasRole := oh.userHasAccess(pubKeyFromAuth, uuid, db.ViewReport)
	if !hasRole {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("Don't have access to view budget history")
		return
	}

	// get the organization budget
	organizationBudget := oh.db.GetWorkspaceBudgetHistory(uuid)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(organizationBudget)
}

func GetPaymentHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	uuid := chi.URLParam(r, "uuid")

	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// if not the organization admin
	hasRole := db.UserHasAccess(pubKeyFromAuth, uuid, db.ViewReport)
	if !hasRole {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode("Don't have access to view payments")
		return
	}

	// get the organization payment history
	paymentHistory := db.DB.GetPaymentHistory(uuid, r)
	paymentHistoryData := []db.PaymentHistoryData{}

	for _, payment := range paymentHistory {
		sender := db.DB.GetPersonByPubkey(payment.SenderPubKey)
		receiver := db.DB.GetPersonByPubkey(payment.ReceiverPubKey)
		paymentData := db.PaymentHistoryData{
			PaymentHistory: payment,
			SenderName:     sender.UniqueName,
			SenderImg:      sender.Img,
			ReceiverName:   receiver.UniqueName,
			ReceiverImg:    receiver.Img,
		}
		paymentHistoryData = append(paymentHistoryData, paymentData)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(paymentHistoryData)
}

func (oh *workspaceHandler) PollBudgetInvoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	uuid := chi.URLParam(r, "uuid")

	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	orgInvoices := oh.db.GetWorkspaceInvoices(uuid)

	for _, inv := range orgInvoices {
		invoiceRes, invoiceErr := oh.getLightningInvoice(inv.PaymentRequest)

		if invoiceErr.Error != "" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(invoiceErr)
			return
		}

		if invoiceRes.Response.Settled {
			if !inv.Status && inv.Type == "BUDGET" {
				oh.db.AddAndUpdateBudget(inv)
				// Update the invoice status
				oh.db.UpdateInvoice(inv.PaymentRequest)
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode("Polled invoices")
}

func GetInvoicesCount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	uuid := chi.URLParam(r, "uuid")

	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	invoiceCount := db.DB.GetWorkspaceInvoicesCount(uuid)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(invoiceCount)
}

func (oh *workspaceHandler) DeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	uuid := chi.URLParam(r, "uuid")

	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	organization := oh.db.GetWorkspaceByUuid(uuid)

	if pubKeyFromAuth != organization.OwnerPubKey {
		msg := "only workspace admin can delete an organization"
		fmt.Println(msg)
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(msg)
		return
	}

	// Update organization to hide and clear certain fields
	if err := oh.db.UpdateWorkspaceForDeletion(uuid); err != nil {
		fmt.Println("Error updating organization:", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Delete all users from the organization
	if err := oh.db.DeleteAllUsersFromWorkspace(uuid); err != nil {
		fmt.Println("Error removing users from organization:", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// soft delete organization
	workspace := oh.db.ChangeWorkspaceDeleteStatus(uuid, true)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(workspace)
}

func (oh *workspaceHandler) UpdateWorkspaceMission(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	workspace := db.Organization{}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	err := json.Unmarshal(body, &workspace)

	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	if pubKeyFromAuth != workspace.OwnerPubKey {
		hasRole := db.UserHasAccess(pubKeyFromAuth, workspace.Uuid, db.EditOrg)
		if !hasRole {
			fmt.Println(pubKeyFromAuth)
			fmt.Println(workspace.OwnerPubKey)
			fmt.Println("mismatched pubkey")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode("Don't have access to Edit workspace")
			return
		}
	}

	// Validate struct data
	err = db.Validate.Struct(workspace)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("Error: did not pass validation test : %s", err)
		json.NewEncoder(w).Encode(msg)
		return
	}

	p, err := oh.db.CreateOrEditWorkspace(workspace)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(p)
}

func (oh *workspaceHandler) UpdateWorkspaceTactics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	workspace := db.Organization{}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	err := json.Unmarshal(body, &workspace)

	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	if pubKeyFromAuth != workspace.OwnerPubKey {
		hasRole := db.UserHasAccess(pubKeyFromAuth, workspace.Uuid, db.EditOrg)
		if !hasRole {
			fmt.Println(pubKeyFromAuth)
			fmt.Println(workspace.OwnerPubKey)
			fmt.Println("mismatched pubkey")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode("Don't have access to Edit workspace")
			return
		}
	}

	// Validate struct data
	err = db.Validate.Struct(workspace)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("Error: did not pass validation test : %s", err)
		json.NewEncoder(w).Encode(msg)
		return
	}

	p, err := oh.db.CreateOrEditWorkspace(workspace)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(p)
}

func (oh *workspaceHandler) UpdateWorkspaceSchematicUrl(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pubKeyFromAuth, _ := ctx.Value(auth.ContextKey).(string)
	if pubKeyFromAuth == "" {
		fmt.Println("no pubkey from auth")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	workspace := db.Organization{}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	err := json.Unmarshal(body, &workspace)

	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	if pubKeyFromAuth != workspace.OwnerPubKey {
		hasRole := db.UserHasAccess(pubKeyFromAuth, workspace.Uuid, db.EditOrg)
		if !hasRole {
			fmt.Println(pubKeyFromAuth)
			fmt.Println(workspace.OwnerPubKey)
			fmt.Println("mismatched pubkey")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode("Don't have access to Edit workspace")
			return
		}
	}

	// Validate struct data
	err = db.Validate.Struct(workspace)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("Error: did not pass validation test : %s", err)
		json.NewEncoder(w).Encode(msg)
		return
	}

	p, err := oh.db.CreateOrEditWorkspace(workspace)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(p)
}

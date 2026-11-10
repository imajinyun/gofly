package app

import (
	"context"

	coreerrors "github.com/imajinyun/gofly/core/errors"
)

// OrganizationFacade combines the department, position, and employee
// services into the single Organization interface consumed by the
// SmartAdmin compatibility adapter. Explicit nil guards avoid the
// typed-nil trap when concrete *coreerrors.Error values cross an
// error-returning interface boundary.
type OrganizationFacade struct {
	departments *DepartmentService
	positions   *PositionService
	employees   *EmployeeService
}

// NewOrganizationFacade constructs the combined organization facade.
func NewOrganizationFacade(departments *DepartmentService, positions *PositionService, employees *EmployeeService) *OrganizationFacade {
	return &OrganizationFacade{departments: departments, positions: positions, employees: employees}
}

func (f *OrganizationFacade) ListDepartments(ctx context.Context) ([]Department, error) {
	v, err := f.departments.List(ctx)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (f *OrganizationFacade) DepartmentTree(ctx context.Context) ([]Department, error) {
	v, err := f.departments.Tree(ctx)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (f *OrganizationFacade) CreateDepartment(ctx context.Context, input DepartmentInput) (Department, error) {
	v, err := f.departments.Create(ctx, input)
	if err != nil {
		return Department{}, err
	}
	return v, nil
}

func (f *OrganizationFacade) UpdateDepartment(ctx context.Context, id uint64, input DepartmentInput) error {
	return guard(f.departments.Update(ctx, id, input))
}

func (f *OrganizationFacade) DeleteDepartment(ctx context.Context, id uint64) error {
	return guard(f.departments.Delete(ctx, id))
}

func (f *OrganizationFacade) ListPositions(ctx context.Context, keyword string) ([]Position, error) {
	v, err := f.positions.List(ctx, keyword)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (f *OrganizationFacade) CreatePosition(ctx context.Context, input PositionInput) (Position, error) {
	v, err := f.positions.Create(ctx, input)
	if err != nil {
		return Position{}, err
	}
	return v, nil
}

func (f *OrganizationFacade) UpdatePosition(ctx context.Context, id uint64, input PositionInput) error {
	return guard(f.positions.Update(ctx, id, input))
}

func (f *OrganizationFacade) DeletePosition(ctx context.Context, id uint64) error {
	return guard(f.positions.Delete(ctx, id))
}

func (f *OrganizationFacade) ListEmployees(ctx context.Context, query EmployeeQuery) (EmployeePage, error) {
	v, err := f.employees.List(ctx, query)
	if err != nil {
		return EmployeePage{}, err
	}
	return v, nil
}

func (f *OrganizationFacade) ListAllEmployees(ctx context.Context, includeDisabled bool) ([]Employee, error) {
	v, err := f.employees.ListAll(ctx, includeDisabled)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (f *OrganizationFacade) EmployeesByDepartment(ctx context.Context, departmentID uint64) ([]Employee, error) {
	v, err := f.employees.ByDepartment(ctx, departmentID)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (f *OrganizationFacade) CreateEmployee(ctx context.Context, input EmployeeInput) (Employee, string, error) {
	v, password, err := f.employees.Create(ctx, input)
	if err != nil {
		return Employee{}, "", err
	}
	return v, password, nil
}

func (f *OrganizationFacade) UpdateEmployee(ctx context.Context, input EmployeeInput) error {
	return guard(f.employees.Update(ctx, input))
}

func (f *OrganizationFacade) SetEmployeeDisabled(ctx context.Context, userID uint64, disabled bool) error {
	return guard(f.employees.SetDisabled(ctx, userID, disabled))
}

func (f *OrganizationFacade) DeleteEmployees(ctx context.Context, ids []uint64) error {
	return guard(f.employees.Delete(ctx, ids))
}

func (f *OrganizationFacade) MoveEmployeesDepartment(ctx context.Context, ids []uint64, departmentID uint64) error {
	return guard(f.employees.MoveDepartment(ctx, ids, departmentID))
}

func (f *OrganizationFacade) ResetEmployeePassword(ctx context.Context, userID uint64, newPassword string) (string, error) {
	v, err := f.employees.ResetPassword(ctx, userID, newPassword)
	if err != nil {
		return "", err
	}
	return v, nil
}

func (f *OrganizationFacade) UpdateEmployeeCenter(ctx context.Context, userID uint64, input EmployeeCenterInput) error {
	return guard(f.employees.UpdateCenter(ctx, userID, input))
}

func (f *OrganizationFacade) UpdateEmployeeAvatar(ctx context.Context, userID uint64, avatar string) error {
	return guard(f.employees.UpdateAvatar(ctx, userID, avatar))
}

// guard converts a possibly typed-nil *coreerrors.Error into a nil error.
func guard(err *coreerrors.Error) error {
	if err == nil {
		return nil
	}
	return err
}
